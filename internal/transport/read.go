package transport

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/readmodel"
	"github.com/0xmhha/trace-agent/internal/store"
)

type endpoint struct{ query, screen string }

func screenRequest(selection object) (endpoint, object, error) {
	endpoints := map[string]endpoint{
		"OVERVIEW": {"overview", "Overview"}, "TASKS": {"tasks", "TaskPage"}, "TASK": {"task", "TaskDetail"}, "AGENTS": {"agents", "AgentPage"}, "AGENT": {"agent", "AgentDetail"}, "PLAN": {"plans", "PlanPage"}, "STEP_ACTIVITIES": {"stepActivities", "ActivityPage"}, "MODULE_GROUPS": {"moduleGroups", "ModuleGroupPage"}, "MODULE_GROUP": {"moduleGroup", "GroupDetail"}, "MODULES": {"modules", "ModulePage"}, "MODULE": {"module", "ModuleDetail"}, "ATTEMPTS": {"attempts", "AttemptPage"}, "ATTEMPT": {"attempt", "AttemptDetail"}, "SOURCES": {"sources", "SourcePage"}, "SOURCE": {"source", "SourceDetail"}, "UNLINKED": {"unlinked", "UnlinkedPage"}, "UNLINKED_DETAIL": {"evidence", "EvidenceDetail"}, "IDENTITIES": {"identities", "IdentityPage"}, "IDENTITY": {"identity", "IdentityDetail"}, "SEQUENCE": {"sequence", "SequencePage"}}
	e, ok := endpoints[str(selection, "kind")]
	if !ok {
		return e, nil, errVariables
	}
	args := maps.Clone(selection)
	delete(args, "kind")
	delete(args, "metric")
	delete(args, "durationBasis")
	task := obj(args, "task")
	entity := obj(args, "entity")
	delete(args, "task")
	delete(args, "entity")
	if e.query == "attempts" {
		if task != nil {
			args["task"] = task
		}
		if entity != nil {
			args["module"] = entity
		}
	} else {
		if task != nil && entity != nil {
			return e, nil, errVariables
		}
		if entity != nil {
			args["key"] = entity
		} else if task != nil {
			args["key"] = task
		}
	}
	for from, to := range map[string]string{"identityId": "id", "moduleKind": "kind", "agentState": "state", "sequenceWindow": "window"} {
		if val, yes := args[from]; yes {
			args[to] = val
			delete(args, from)
		}
	}
	return e, args, nil
}
func normalized(data any) (any, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var x any
	err = json.Unmarshal(raw, &x)
	return x, err
}
func (s *Server) data(ctx context.Context, v view, op string, args object) (any, error) {
	if current, err := s.db.Point(ctx); err != nil || v.p.Epoch != current.Epoch || v.p.Revision > current.Revision {
		return nil, errScope
	}
	if (op == "sourceModelAttempts" || op == "sourceModelAttempt") && (len(v.f.Clients) != 0 || len(v.f.Tasks) != 0 || len(v.f.Sources) != 1 || v.f.Sources[0] != str(args, "sourceId")) {
		return nil, errScope
	}
	parsed, bindings, err := unsealPages(s, v, op, args)
	if err != nil {
		return nil, err
	}
	var data any
	catalog, available := s.db.(sourceCatalog)
	if available && (op == "source" || op == "sources") {
		data, err = s.sourceData(ctx, catalog, v, op, parsed)
	} else {
		data, err = s.modelData(ctx, v, op, parsed)
	}
	if err != nil {
		return nil, err
	}
	if d, ok := data.(object); ok {
		if op == "evidence" && str(obj(d, "evidence"), "link") == "UNLINKED" && (len(v.f.Sources) != 1 || v.f.Sources[0] != str(obj(d, "evidence"), "sourceId")) {
			return nil, nil
		}
		if err = s.addBodies(ctx, v.p, op, d); err != nil {
			return nil, err
		}
	}
	signPages(s, v, data, "page", bindings)
	return data, nil
}

func (p *peer) next(ctx context.Context, id string, op operation, data any, epoch int64) error {
	result, err := op.project(data)
	if err != nil {
		return err
	}
	return p.send(ctx, envelope{ID: id, Type: "next", Payload: object{"data": object{op.name: result}}}, epoch)
}
func (p *peer) execute(ctx context.Context, id string, op operation, args object) error {
	switch op.name {
	case "watch":
		return p.watch(ctx, id, op, obj(args, "input"))
	case "currentStatus":
		return p.status(ctx, id, op)
	}
	current, err := p.s.db.Point(ctx)
	if err != nil {
		return err
	}
	if !p.isAck(current.Epoch) && op.name != "acknowledgeEpoch" {
		return errAuth
	}
	if op.kind == "mutation" || op.name == "previewDelete" {
		result, epoch, err := p.manageOperation(ctx, op.name, args)
		if err != nil {
			return err
		}
		return p.next(ctx, id, op, result, epoch)
	}
	v, err := p.s.fixed(obj(args, "scope"))
	if err != nil || current.Epoch != v.p.Epoch || v.p.Revision > current.Revision {
		return errScope
	}
	if op.name == "sourceScope" {
		source := str(args, "sourceId")
		if len(v.f.Sources) > 0 && !slices.Contains(v.f.Sources, source) {
			return errScope
		}
		catalog, ok := p.s.db.(sourceCatalog)
		if !ok {
			return errScope
		}
		infos, err := catalog.Sources(ctx, v.p)
		if err != nil {
			return err
		}
		allowed := slices.ContainsFunc(infos, func(i store.SourceInfo) bool { return i.ID == source && sourceAllowed(v, i) })
		if !allowed {
			return errScope
		}
		v.f.Clients = []string{}
		v.f.Tasks = []string{}
		v.f.Sources = []string{source}
		v.sample = readmodel.Sample{}
		return p.next(ctx, id, op, v.stamp(p.s, p.s.resume(v, digest(v.f))), v.p.Epoch)
	}
	queryArgs := maps.Clone(args)
	delete(queryArgs, "scope")
	data, err := p.s.data(ctx, v, op.name, queryArgs)
	if err != nil {
		return err
	}
	result := object{"stamp": v.stamp(p.s, p.s.resume(v, digest([]any{op.name, v.f, queryArgs}))), "data": data}
	if data == nil {
		payload, err := op.project(result)
		if err != nil {
			return err
		}
		return p.send(ctx, envelope{ID: id, Type: "next", Payload: object{"data": object{op.name: payload}, "errors": []object{{"message": "detail unavailable", "path": []string{op.name, "data"}, "extensions": object{"code": "NOT_FOUND"}}}}}, v.p.Epoch)
	}
	return p.next(ctx, id, op, result, v.p.Epoch)
}
func (s *Server) resume(v view, binding string) string {
	return s.seal(claim{Kind: "resume", Rev: v.p.Revision, Epoch: v.p.Epoch, Bind: binding, Sample: &v.sample})
}
func (p *peer) reset(ctx context.Context, id string, op operation, why string, point store.Point) error {
	return p.next(ctx, id, op, object{"__typename": "Reset", "reason": why, "current": pointMap(point)}, point.Epoch)
}

type floorReader interface {
	ReplayFloor(context.Context) (int64, error)
}
type changeReader interface {
	ChangedSources(context.Context, int64, int64) ([]string, error)
}

func (p *peer) watch(ctx context.Context, id string, op operation, input object) error {
	scope := obj(input, "scope")
	sel := obj(input, "selection")
	e, args, err := screenRequest(sel)
	if err != nil {
		return err
	}
	// Keep only arguments declared on the corresponding fixed-scope SDL field.
	def := p.s.cat.schema.Query.Fields.ForName(e.query)
	for k := range args {
		if def.Arguments.ForName(k) == nil {
			if k == "activeTasksOnly" && args[k] == false || k == "window" && args[k] == "FIRST" {
				delete(args, k)
				continue
			}
			return errVariables
		}
	}
	for _, a := range def.Arguments {
		if a.Name != "scope" && a.Type.NonNull && args[a.Name] == nil {
			return errVariables
		}
	}
	if e.query == "sequence" && args["window"] == nil {
		args["window"] = "FIRST"
	}
	frozen := str(scope, "mode") == "FROZEN"
	follow := str(scope, "liveWindow") == "FOLLOW_END"
	if frozen && follow {
		return errScope
	}
	f, err := readFilter(obj(scope, "filter"))
	if err != nil {
		return err
	}
	if follow && f.Until.After(time.Now()) {
		return errScope
	}
	for k := range args {
		if isPage(k) && !frozen && str(obj(args, k), "after") != "" {
			return errScope
		}
	}
	base := view{f: f}
	basis := str(sel, "durationBasis")
	if basis != "" && str(sel, "metric") != "LATENCY" {
		return errVariables
	}
	base.sample.Basis = basis
	if metric := str(sel, "metric"); metric != "" {
		base.sample.Metric = metric
	}
	if frozen {
		base, err = p.s.fixed(scope)
		if basis != "" && basis != base.sample.Basis {
			return errScope
		}
		if metric := str(sel, "metric"); metric != "" && metric != base.sample.Metric {
			return errScope
		}
		if err != nil {
			return err
		}
	} else if scope["point"] != nil || scope["cohort"] != nil {
		return errScope
	}
	bind := digest([]any{base.f, base.sample, frozen, follow, sel})
	last := store.Point{}
	current, err := p.s.db.Point(ctx)
	if err != nil {
		return err
	}
	if !p.isAck(current.Epoch) || frozen && base.p.Epoch != current.Epoch {
		return p.reset(ctx, id, op, "EPOCH_CHANGED", current)
	}
	if token := str(input, "resume"); token != "" {
		c, err := p.s.open(token, "resume")
		if err != nil {
			return p.reset(ctx, id, op, "REPLAY_EXPIRED", current)
		}
		if c.Epoch != current.Epoch {
			return p.reset(ctx, id, op, "EPOCH_CHANGED", current)
		}
		if c.Bind != bind {
			return p.reset(ctx, id, op, "FILTER_CHANGED", current)
		}
		if c.Rev > current.Revision || frozen && c.Rev != base.p.Revision {
			return p.reset(ctx, id, op, "REVISION_GONE", current)
		}
		if floors, ok := p.s.db.(floorReader); ok {
			floor, err := floors.ReplayFloor(ctx)
			if err != nil {
				return err
			}
			if c.Rev < floor {
				return p.reset(ctx, id, op, "REPLAY_EXPIRED", current)
			}
		}
		last = store.Point{Revision: c.Rev, Epoch: c.Epoch}
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		current, err = p.s.db.Point(ctx)
		if err != nil {
			return err
		}
		if last.Epoch != 0 && last.Epoch != current.Epoch || !p.isAck(current.Epoch) {
			return p.reset(ctx, id, op, "EPOCH_CHANGED", current)
		}
		v := base
		if !frozen {
			v.p = current
		} else if v.p.Revision > current.Revision {
			return p.reset(ctx, id, op, "REVISION_GONE", current)
		}
		if last != v.p {
			changed := true
			if r, ok := p.s.db.(changeReader); ok && last.Epoch != 0 && len(f.Sources) > 0 && !follow {
				ids, err := r.ChangedSources(ctx, last.Revision, v.p.Revision)
				if err != nil {
					return err
				}
				if len(ids) > 0 && !slices.Contains(ids, "*") {
					changed = slices.ContainsFunc(ids, func(id string) bool { return slices.Contains(f.Sources, id) })
				}
			}
			if changed {
				if follow {
					at, err := p.s.db.CommittedAt(ctx, v.p.Revision)
					if err != nil {
						return err
					}
					end := at.Add(time.Millisecond)
					if end.After(v.f.Until) {
						v.f.Until = end.UTC()
					}
				}
				data, err := p.s.data(ctx, v, e.query, args)
				if err == nil {
					screen, _ := data.(object)
					if screen == nil {
						return errMissing
					}
					if e.screen == "UnlinkedPage" {
						screen = object{"evidence": screen}
					}
					screen["__typename"] = e.screen
					frame := object{"__typename": "Snapshot", "screen": screen, "stamp": v.stamp(p.s, p.s.resume(v, bind))}
					if last.Epoch != 0 {
						frame = object{"__typename": "Replacement", "base": pointMap(last), "snapshot": frame}
					}
					err = p.next(ctx, id, op, frame, v.p.Epoch)
				}
				if err != nil {
					latest, pe := p.s.db.Point(ctx)
					if pe == nil && latest.Epoch != v.p.Epoch {
						return p.reset(ctx, id, op, "EPOCH_CHANGED", latest)
					}
					if errors.Is(err, errOverflow) {
						return p.reset(ctx, id, op, "OVERFLOW", latest)
					}
					return err
				}
				p.mu.Lock()
				p.displayed = v.p
				p.mu.Unlock()
				last = v.p
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (p *peer) status(ctx context.Context, id string, op operation) error {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	// Start with a full status even if the caller reconnects after a process restart.
	var prior store.Point
	var priorNew bool
	for {
		pt, err := p.s.db.Point(ctx)
		if err != nil {
			return err
		}
		p.mu.Lock()
		displayed := p.displayed
		p.mu.Unlock()
		fresh := displayed.Epoch != 0 && displayed != pt
		if pt != prior || fresh != priorNew {
			sources := []object{}
			if c, ok := p.s.db.(sourceCatalog); ok {
				infos, err := c.Sources(ctx, pt)
				if err != nil {
					return err
				}
				for _, info := range infos {
					if info.Health == "" || info.CheckedMS == nil {
						continue
					}
					entry := object{"sourceId": info.ID, "health": strings.ToUpper(info.Health), "checkedAt": iso(time.UnixMilli(*info.CheckedMS))}
					if info.LastEventMS != nil {
						entry["lastEventAt"] = iso(time.UnixMilli(*info.LastEventMS))
					}
					sources = append(sources, entry)
				}
			}
			err = p.next(ctx, id, op, object{"seq": strconv.FormatUint(p.s.statusSeq.Add(1), 10), "latest": pointMap(pt), "sources": sources, "newData": fresh}, pt.Epoch)
			if err != nil {
				return err
			}
			prior, priorNew = pt, fresh
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
