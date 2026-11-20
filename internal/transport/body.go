package transport

import (
	"context"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

const bodyLimit = 128 << 10

type bodyReader interface {
	BodyFields(context.Context, store.Point, int64) ([]collect.BodySlot, error)
}
type availabilityReader interface {
	BodyAvailability(context.Context, store.Point, int64) (string, error)
}

// One response owns its resolved text. No text enters the reusable read model.
type bodyView struct {
	ctx    context.Context
	db     bodyReader
	p      store.Point
	bytes  int
	loaded map[string][]collect.BodySlot
}

func (b *bodyView) slots(id string) ([]collect.BodySlot, error) {
	if a, ok := b.loaded[id]; ok {
		return a, nil
	}
	n, err := strconv.ParseInt(id, 10, 64)
	if err != nil || n < 1 {
		return nil, errMissing
	}
	a, err := b.db.BodyFields(b.ctx, b.p, n)
	if err == nil {
		b.loaded[id] = a
	}
	return a, err
}

func (b *bodyView) field(id string, slot collect.BodySlot, edge object) object {
	purpose, ord, _ := strings.Cut(slot.Field, ".")
	ordinal, _ := strconv.Atoi(ord)
	f := object{"evidenceId": id, "purpose": purpose, "ordinal": ordinal, "grade": "DIRECT", "observer": "CLIENT"}
	if slot.Role != "" {
		f["conversationRole"] = strings.ToUpper(slot.Role)
	}
	for _, k := range []string{"observer", "actor", "direction", "action", "grade"} {
		if val := str(edge, k); val != "" {
			f[k] = val
		}
	}
	switch slot.Status {
	case "sanitized", "truncated":
		text := slot.Text
		f["state"] = "AVAILABLE"
		if slot.Status == "truncated" {
			f["state"], f["truncation"] = "TRUNCATED", "SOURCE_LIMIT"
		}
		if len(text) > b.bytes {
			cut := b.bytes
			for cut > 0 && !utf8.RuneStart(text[cut]) {
				cut--
			}
			text = text[:cut]
			f["state"], f["truncation"] = "TRUNCATED", "DISPLAY_LIMIT"
			if slot.Status == "truncated" {
				f["truncation"] = "SOURCE_AND_DISPLAY"
			}
		}
		f["text"] = text
		b.bytes -= len(text)
	case "redaction_failed":
		f["state"], f["reason"] = "FAILED", "redaction_failed"
	case "deleted":
		f["state"], f["reason"] = "DELETED", "deleted"
	case "revoked":
		f["state"], f["reason"] = "REVOKED", "consent_revoked"
	default:
		f["state"], f["reason"] = "DISABLED", "collection_disabled"
	}
	return f
}

func anyList(m object, k string) []any      { a, _ := m[k].([]any); return a }
func number(m object, k string) (int, bool) { n, ok := m[k].(float64); return int(n), ok }

func (b *bodyView) evidence(d object) error {
	id := str(obj(d, "evidence"), "id")
	slots, err := b.slots(id)
	if err != nil {
		return err
	}
	if len(slots) == 0 {
		if reader, ok := b.db.(availabilityReader); ok {
			n, _ := strconv.ParseInt(id, 10, 64)
			state, err := reader.BodyAvailability(b.ctx, b.p, n)
			if err != nil {
				return err
			}
			d["fields"] = []any{object{"purpose": "body", "evidenceId": id, "state": state, "ordinal": 0, "grade": "UNKNOWN", "reason": strings.ToLower(state)}}
		}
		return nil
	}
	fields := make([]any, 0, len(slots))
	for _, slot := range slots {
		fields = append(fields, b.field(id, slot, nil))
	}
	d["fields"] = fields
	return nil
}

func (b *bodyView) attempt(d object) error {
	var request, response []any
	for _, raw := range anyList(d, "edges") {
		edge, _ := raw.(object)
		direction := str(edge, "direction")
		model := direction == "MODEL_REQUEST" || direction == "MODEL_RESPONSE"
		index, hasIndex := number(edge, "_bodyOrdinal")
		if !model && !hasIndex {
			continue
		}
		for _, ref := range anyList(edge, "evidenceIds") {
			id, _ := ref.(string)
			slots, err := b.slots(id)
			if err != nil {
				return err
			}
			for _, slot := range slots {
				purpose, ordinal, hasOrdinal := strings.Cut(slot.Field, ".")
				if !model && (hasOrdinal && ordinal != strconv.Itoa(index) || !hasOrdinal && index != 0) {
					continue
				}
				switch direction {
				case "TOOL_REQUEST":
					if purpose == "mission" {
						request = append(request, b.field(id, slot, edge))
					}
				case "TOOL_RESULT":
					if purpose == "result" {
						response = append(response, b.field(id, slot, edge))
					}
				case "MODEL_REQUEST":
					request = append(request, b.field(id, slot, edge))
				case "MODEL_RESPONSE":
					response = append(response, b.field(id, slot, edge))
				}
			}
		}
	}
	if len(request) > 0 {
		d["request"] = request
	}
	if len(response) > 0 {
		d["response"] = response
	}
	return nil
}

func (b *bodyView) stepOrMission(v any) error {
	switch d := v.(type) {
	case []any:
		for _, row := range d {
			if err := b.stepOrMission(row); err != nil {
				return err
			}
		}
	case object:
		if id := str(d, "_missionEvidence"); id != "" {
			ordinal, ok := number(d, "_missionOrdinal")
			if ok {
				slots, err := b.slots(id)
				if err != nil {
					return err
				}
				for _, slot := range slots {
					if slot.Field == "mission."+strconv.Itoa(ordinal) || (ordinal == 0 && slot.Field == "mission") {
						d["mission"] = b.field(id, slot, nil)
						break
					}
				}
			}
		}
		if ordinal, ok := number(d, "ordinal"); ok && obj(d, "title") != nil {
			for _, ref := range anyList(d, "evidenceIds") {
				id, _ := ref.(string)
				slots, err := b.slots(id)
				if err != nil {
					return err
				}
				for _, slot := range slots {
					if slot.Field == "step."+strconv.Itoa(ordinal) {
						d["title"] = b.field(id, slot, nil)
						return nil
					}
				}
			}
		}
		// Visit only projections containing explicitly resolved field ownership.
		for _, k := range []string{"items", "versions", "steps", "currentSteps", "assignments"} {
			if err := b.stepOrMission(d[k]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) addBodies(ctx context.Context, p store.Point, op string, d object) error {
	db, ok := s.db.(bodyReader)
	if !ok {
		return nil
	}
	b := bodyView{ctx: ctx, db: db, p: p, bytes: bodyLimit, loaded: map[string][]collect.BodySlot{}}
	switch op {
	case "evidence":
		return b.evidence(d)
	case "attempt", "sourceModelAttempt":
		return b.attempt(d)
	case "task", "plans", "agent", "assignment", "sequenceInspector", "identity":
		return b.stepOrMission(d)
	}
	return nil
}
