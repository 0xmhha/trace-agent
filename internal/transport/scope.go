package transport

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/0xmhha/trace-agent/internal/readmodel"
	"github.com/0xmhha/trace-agent/internal/store"
)

var errScope = errors.New("invalid scope")

type filter struct {
	From, Until             time.Time
	Clients, Sources, Tasks []string
}

func list(m object, key string) []string {
	raw, _ := m[key].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
func readFilter(m object) (filter, error) {
	i := obj(m, "interval")
	from, e1 := time.Parse(time.RFC3339Nano, str(i, "from"))
	until, e2 := time.Parse(time.RFC3339Nano, str(i, "until"))
	if e1 != nil || e2 != nil || !from.Before(until) {
		return filter{}, errScope
	}
	return filter{from.UTC(), until.UTC(), list(m, "clients"), list(m, "sources"), list(m, "tasks")}, nil
}
func pointMap(p store.Point) object {
	return object{"revision": strconv.FormatInt(p.Revision, 10), "epoch": strconv.FormatInt(p.Epoch, 10)}
}
func readPoint(m object) (store.Point, error) {
	r, e := strconv.ParseInt(str(m, "revision"), 10, 64)
	ep, e2 := strconv.ParseInt(str(m, "epoch"), 10, 64)
	if e != nil || e2 != nil || r < 0 || ep < 1 {
		return store.Point{}, errScope
	}
	return store.Point{Revision: r, Epoch: ep}, nil
}
func iso(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func digest(v any) string {
	raw, _ := json.Marshal(v)
	d := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(d[:])
}

type claim struct {
	Kind   string            `json:"k"`
	Rev    int64             `json:"r"`
	Epoch  int64             `json:"e"`
	Bind   string            `json:"b"`
	Sample *readmodel.Sample `json:"s,omitempty"`
	Offset string            `json:"o,omitempty"`
}

func (s *Server) seal(v claim) string {
	raw, _ := json.Marshal(v)
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("trace-agent/graphql/v1.3/"))
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(append(mac.Sum(nil), raw...))
}
func (s *Server) open(token, kind string) (claim, error) {
	var v claim
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) <= sha256.Size {
		return v, errScope
	}
	msg := raw[sha256.Size:]
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte("trace-agent/graphql/v1.3/"))
	_, _ = mac.Write(msg)
	if !hmac.Equal(raw[:sha256.Size], mac.Sum(nil)) || json.Unmarshal(msg, &v) != nil || v.Kind != kind {
		return claim{}, errScope
	}
	return v, nil
}

type view struct {
	f      filter
	p      store.Point
	sample readmodel.Sample
}

func (v view) token(s *Server, sample readmodel.Sample) string {
	return s.seal(claim{Kind: "cohort", Rev: v.p.Revision, Epoch: v.p.Epoch, Bind: digest(v.f), Sample: &sample})
}
func (v view) stamp(s *Server, resume string) object {
	return object{"point": pointMap(v.p), "cohort": v.token(s, v.sample), "computedAt": iso(time.Now()), "resume": resume,
		"effectiveInterval": object{"from": iso(v.f.From), "until": iso(v.f.Until)}}
}
func (s *Server) fixed(scope object) (view, error) {
	f, err := readFilter(obj(scope, "filter"))
	if err != nil {
		return view{}, err
	}
	p, err := readPoint(obj(scope, "point"))
	if err != nil {
		return view{}, err
	}
	c, err := s.open(str(scope, "cohort"), "cohort")
	if err != nil || c.Epoch != p.Epoch || c.Rev != p.Revision || c.Bind != digest(f) || c.Sample == nil {
		return view{}, errScope
	}
	return view{f, p, *c.Sample}, nil
}

func options(v view, s *Server) readmodel.Options {
	return readmodel.Options{From: v.f.From, Until: v.f.Until, Clients: v.f.Clients, Sources: v.f.Sources, Tasks: v.f.Tasks, Cohort: v.token(s, v.sample), Sample: v.sample, Token: func(sample readmodel.Sample) string { return v.token(s, sample) }}
}
func isPage(k string) bool {
	return k == "page" || k == "relationsPage" || k == "childrenPage" || k == "attemptsPage" || k == "evidencePage" || k == "assignmentsPage" || k == "waitsPage"
}
func unsealPages(s *Server, v view, op string, args object) (object, map[string]string, error) {
	// Bind the cursor to all semantic parameters. A relations cursor also belongs
	// to the event page on which its endpoints were selected.
	base := maps.Clone(args)
	for k := range base {
		if k == "scope" || isPage(k) {
			delete(base, k)
		}
	}
	out := maps.Clone(args)
	bindings := map[string]string{}
	for _, k := range []string{"page", "relationsPage", "childrenPage", "attemptsPage", "evidencePage", "assignmentsPage", "waitsPage"} {
		p := obj(args, k)
		n := 50
		if x, ok := p["first"].(int); ok {
			n = x
		}
		binding := []any{op, base, v.f, v.sample, k, n}
		if k == "relationsPage" {
			binding = append(binding, obj(args, "page"))
		}
		b := digest(binding)
		bindings[k] = b
		if p == nil {
			continue
		}
		page := maps.Clone(p)
		out[k] = page
		if after := str(page, "after"); after != "" {
			c, err := s.open(after, "page")
			if err != nil || c.Rev != v.p.Revision || c.Epoch != v.p.Epoch || c.Bind != b {
				return nil, nil, errScope
			}
			page["after"] = c.Offset
		}
	}
	return out, bindings, nil
}
func signPages(s *Server, v view, raw any, field string, bindings map[string]string) {
	switch val := raw.(type) {
	case []any:
		for _, item := range val {
			signPages(s, v, item, field, bindings)
		}
	case object:
		for k, child := range val {
			if k == "endCursor" || k == "previousPage" {
				if off, ok := child.(string); ok && off != "" {
					val[k] = s.seal(claim{Kind: "page", Rev: v.p.Revision, Epoch: v.p.Epoch, Bind: bindings[field], Offset: off})
				}
				continue
			}
			next := field
			switch k {
			case "relationPage":
				next = "relationsPage"
			case "children":
				next = "childrenPage"
			case "attempts":
				if _, ok := val["module"]; ok {
					next = "attemptsPage"
				}
			case "assignments":
				next = "assignmentsPage"
			case "evidence":
				if _, ok := val["identity"]; ok {
					next = "evidencePage"
				}
				if _, ok := val["attempt"]; ok {
					if _, yes := obj(val, "attempt")["sourceId"]; yes {
						next = "evidencePage"
					}
				}
			case "waits":
				next = "waitsPage"
			}
			signPages(s, v, child, next, bindings)
		}
	}
}
