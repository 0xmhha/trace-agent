package transport

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/0xmhha/trace-agent/internal/readmodel"
	"github.com/0xmhha/trace-agent/internal/store"
)

type modelCache struct {
	mu      sync.Mutex
	point   store.Point
	records []store.Record
	model   *readmodel.Model
	scope   string
}

func (s *Server) model(ctx context.Context, v view) (*readmodel.Model, error) {
	c := &s.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key := digest([]any{v.f, v.sample})
	if c.point != v.p {
		rows, err := s.db.Records(ctx, v.p)
		if err != nil {
			return nil, err
		}
		c.records, c.point, c.model = rows, v.p, nil
	}
	if c.model == nil || c.scope != key {
		c.model = readmodel.New(c.records, options(v, s))
		c.scope = key
	}
	return c.model, nil
}

func (s *Server) modelData(ctx context.Context, v view, op string, args object) (any, error) {
	m, err := s.model(ctx, v)
	if err != nil {
		return nil, err
	}
	raw, err := m.Query(op, args)
	if err != nil {
		return nil, err
	}
	return normalized(raw)
}

type sourceCatalog interface {
	Sources(context.Context, store.Point) ([]store.SourceInfo, error)
}

func sourceAllowed(v view, i store.SourceInfo) bool {
	return (len(v.f.Sources) == 0 || slices.Contains(v.f.Sources, i.ID)) && (len(v.f.Clients) == 0 || slices.Contains(v.f.Clients, strings.ToUpper(i.Client)))
}

func sourceRow(i store.SourceInfo) object {
	row := object{"id": i.ID, "client": strings.ToUpper(i.Client), "kind": i.Kind, "adapterVersion": "UNKNOWN", "permission": "REGISTERED"}
	if i.AdapterVersion != "" {
		row["adapterVersion"] = i.AdapterVersion
	}
	if i.ClientVersion != "" {
		row["clientVersion"] = i.ClientVersion
	}
	if !i.Enabled {
		row["permission"] = "PAUSED"
	}
	if i.CheckedMS != nil {
		row["asOfCheckedAt"] = iso(time.UnixMilli(*i.CheckedMS))
	}
	if i.LastObservedMS != nil {
		row["lastAt"] = iso(time.UnixMilli(*i.LastObservedMS))
	}
	if i.Health != "" {
		row["asOfHealth"] = strings.ToUpper(i.Health)
	}
	return row
}

func (s *Server) sourceData(ctx context.Context, c sourceCatalog, v view, op string, args object) (any, error) {
	if len(v.f.Tasks) > 0 || v.sample.TaskID != "" || (v.sample.Metric != "" && v.sample.Metric != "ALL") {
		return nil, nil
	}
	infos, err := c.Sources(ctx, v.p)
	if err != nil {
		return nil, err
	}
	items := []any{}
	for _, i := range infos {
		if !sourceAllowed(v, i) {
			continue
		}
		if op == "sources" {
			items = append(items, sourceRow(i))
			continue
		}
		if i.ID != str(args, "sourceId") {
			continue
		}
		raw, err := s.modelData(ctx, v, op, args)
		if err != nil {
			return nil, err
		}
		detail, _ := raw.(object)
		if detail == nil {
			detail = object{"source": sourceRow(i), "unlinkedCount": 0, "gaps": []any{}, "capabilities": []any{object{"code": "metadata", "state": "UNVERIFIED"}}, "pickerRef": i.ID}
		}
		detail["source"] = sourceRow(i)
		caps := capabilities(i)
		if g, ok := s.db.(interface {
			SourceGaps(context.Context, store.Point, string, int64, int64) ([]store.SourceGap, bool, error)
		}); ok {
			gaps, more, err := g.SourceGaps(ctx, v.p, i.ID, v.f.From.UnixMilli(), v.f.Until.UnixMilli())
			if err != nil {
				return nil, err
			}
			view := make([]any, 0, len(gaps))
			for _, gap := range gaps {
				row := object{"from": iso(time.UnixMilli(gap.FromMS)), "reason": gapReason(gap.Reason)}
				if gap.UntilMS != nil {
					row["until"] = iso(time.UnixMilli(*gap.UntilMS))
				}
				view = append(view, row)
			}
			detail["gaps"] = view
			if more {
				caps = append(caps, capability("gap_history", "PARTIAL", "The latest 200 gaps in the selected period are shown."))
			}
		}
		detail["capabilities"] = caps
		detail["consent"], detail["enabled"], detail["retentionDays"] = i.Consent, i.Enabled, i.RetentionDays
		if i.FromMS != nil {
			detail["historyFrom"] = iso(time.UnixMilli(*i.FromMS))
		}
		if i.UntilMS != nil {
			detail["historyUntil"] = iso(time.UnixMilli(*i.UntilMS))
		}
		return detail, nil
	}
	if op == "source" {
		return nil, nil
	}
	p := obj(args, "page")
	offset, _ := strconv.Atoi(str(p, "after"))
	first, ok := p["first"].(int)
	if !ok {
		first = 50
	}
	start := min(max(0, offset), len(items))
	end := min(start+first, len(items))
	page := object{"hasNext": end < len(items), "total": len(items)}
	if end < len(items) {
		page["endCursor"] = strconv.Itoa(end)
	}
	return object{"items": items[start:end], "page": page}, nil
}

// An old metadata snapshot can reference erased observations. Release it when
// privacy changes, even if this desktop window never opens another read.
func (s *Server) discardBefore(epoch int64) {
	c := &s.cache
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.point.Epoch < epoch {
		c.point, c.records, c.model, c.scope = store.Point{}, nil, nil, ""
	}
}
