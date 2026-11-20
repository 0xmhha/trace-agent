package transport

import (
	"context"
	"crypto/rand"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/store"
)

type manager interface {
	SetConsent(context.Context, string, bool) (store.Point, error)
	SetEnabled(context.Context, string, bool) (store.Point, error)
	SetRetention(context.Context, string, int) (store.Point, error)
	Preview(context.Context, store.Filter) (store.Preview, error)
	Delete(context.Context, store.Filter, store.Preview) (store.DeleteResult, error)
}

func (p *peer) manageOperation(ctx context.Context, op string, args object) (any, int64, error) {
	if op == "acknowledgeEpoch" {
		current, err := p.s.db.Point(ctx)
		if err != nil {
			return nil, 0, err
		}
		if str(args, "epoch") != pointMap(current)["epoch"] {
			return nil, 0, errScope
		}
		p.mu.Lock()
		p.acknowledged = current.Epoch
		p.mu.Unlock()
		return object{"point": pointMap(current)}, current.Epoch, nil
	}
	if !p.manage {
		return nil, 0, errAuth
	}
	m, ok := p.s.db.(manager)
	if !ok {
		return nil, 0, errAuth
	}
	var pt store.Point
	var err error
	source := str(args, "sourceId")
	switch op {
	case "attachSource", "setHistory":
		p.s.mu.Lock()
		picker := p.s.picker
		p.s.mu.Unlock()
		if picker == nil {
			return nil, 0, errAuth
		}
		v := args
		rangeField := "interval"
		if op == "attachSource" {
			v = obj(args, "input")
			rangeField = "history"
		}
		action := PickerAction{Operation: op, PickerToken: str(v, "pickerToken"), SourceID: source, Client: str(v, "client")}
		action.RetentionDays, _ = v["retentionDays"].(int)
		if op == "attachSource" && (action.RetentionDays < 1 || action.RetentionDays > 36500) {
			return nil, 0, errVariables
		}
		if i := obj(v, rangeField); i != nil {
			f, e := readFilter(object{"interval": i})
			if e != nil {
				return nil, 0, e
			}
			action.From, action.Until = f.From, f.Until
		}
		pt, err = picker(ctx, action)
	case "setConsent":
		value, _ := args["enabled"].(bool)
		pt, err = m.SetConsent(ctx, source, value)
	case "setSourceEnabled":
		value, _ := args["enabled"].(bool)
		pt, err = m.SetEnabled(ctx, source, value)
	case "setRetention":
		days, _ := args["days"].(int)
		if days < 1 || days > 36500 {
			return nil, 0, errVariables
		}
		pt, err = m.SetRetention(ctx, source, days)
	case "deleteTargets":
		reader, ok := p.s.db.(interface {
			DeleteTargets(context.Context, store.Filter, string, string) ([]string, bool, store.Point, error)
		})
		if !ok {
			return nil, 0, errScope
		}
		window, err := readFilter(args)
		if err != nil {
			return nil, 0, err
		}
		ids, more, at, err := reader.DeleteTargets(ctx, store.Filter{Source: source, Kind: "all", FromMS: window.From.UnixMilli(), UntilMS: window.Until.UnixMilli()}, str(args, "target"), str(args, "after"))
		if err != nil {
			return nil, 0, err
		}
		result := make([]any, len(ids))
		for i, id := range ids {
			result[i] = id
		}
		return object{"ids": result, "hasNext": more}, at.Epoch, nil
	case "previewDelete":
		input := obj(args, "input")
		for _, k := range []string{"sessionId", "taskId"} {
			if _, supplied := input[k]; supplied && str(input, k) == "" {
				return nil, 0, errVariables
			}
		}
		f, e := readFilter(input)
		if e != nil {
			return nil, 0, e
		}
		scope := store.Filter{Source: str(input, "sourceId"), Session: str(input, "sessionId"), Task: str(input, "taskId"), FromMS: f.From.UnixMilli(), UntilMS: f.Until.UnixMilli(), Kind: strings.ToLower(str(input, "kind"))}
		pv, err := m.Preview(ctx, scope)
		if err != nil {
			return nil, 0, err
		}
		token := rand.Text()
		expires := time.Now().Add(time.Minute)
		p.mu.Lock()
		for k, r := range p.receipts {
			if !r.until.After(time.Now()) {
				delete(p.receipts, k)
			}
		}
		if len(p.receipts) >= 32 {
			p.mu.Unlock()
			return nil, 0, errOverflow
		}
		p.receipts[token] = receipt{scope, pv, expires}
		p.mu.Unlock()
		return object{"scope": scopeResult(scope), "receipt": token, "evidenceCount": pv.Evidence, "bodyCount": pv.Bodies, "expiresAt": iso(expires)}, pv.Point.Epoch, nil
	case "confirmDelete":
		id := str(args, "receipt")
		p.mu.Lock()
		r, ok := p.receipts[id]
		delete(p.receipts, id)
		p.mu.Unlock()
		if !ok || !r.until.After(time.Now()) {
			return nil, 0, errScope
		}
		result, err := m.Delete(ctx, r.f, r.preview)
		if err != nil {
			return nil, 0, err
		}
		p.s.discardBefore(result.Point.Epoch)
		return object{"scope": scopeResult(r.f), "point": pointMap(result.Point), "evidenceRemoved": result.Evidence, "bodiesRemoved": result.Bodies, "factsRecomputed": result.Facts}, result.Point.Epoch, nil
	default:
		return nil, 0, errVariables
	}
	if err != nil {
		return nil, 0, err
	}
	p.s.discardBefore(pt.Epoch)
	return object{"point": pointMap(pt)}, pt.Epoch, nil
}
func scopeResult(f store.Filter) object {
	scope := object{"sourceId": f.Source, "from": iso(time.UnixMilli(f.FromMS)), "until": iso(time.UnixMilli(f.UntilMS)), "kind": strings.ToUpper(f.Kind)}
	if f.Session != "" {
		scope["sessionId"] = f.Session
	}
	if f.Task != "" {
		scope["taskId"] = f.Task
	}
	return scope
}
