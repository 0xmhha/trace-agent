package readmodel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

type row = map[string]any

// Sample identifies the denominator carried by a measurement into a drill-down.
type Sample struct {
	Metric, TaskID, ModuleID, Basis string
	FromMS, UntilMS                 int64
}

// Options scopes every read, including details reached by direct IDs.
// Token signs measurement selections for callers that implement fixed scopes.
type Options struct {
	From, Until             time.Time
	Clients, Sources, Tasks []string
	Cohort                  string
	Sample                  Sample
	Token                   func(Sample) string
}

// FactEvent is an evidence-backed change. Time values are Unix milliseconds.
// Missing occurrence times stay nil; they cannot place an event on a timeline.
type FactEvent = store.FactProjection

// Relation ties the event to an explicitly supported predecessor.
type Relation = store.FactRelation

type attempt struct {
	resultConflict                                             bool
	id, task, actor, module, name, kind, server, result, phase string
	start, end, until                                          *int64
	reported                                                   *float64
	supports                                                   []string
	events                                                     []FactEvent
}
type identity struct {
	id, parent, client string
	supports           []string
	conflict           bool
}
type task struct {
	id, session, client, source, root string
	events                            []FactEvent
	attempts                          map[string]*attempt
	agents                            map[string]agent
	plans                             []FactEvent
	last                              *int64
	state, grade                      string
	stateUntil                        *int64
	stateIDs                          []string
	project                           string
	projectEvidence                   []string
}
type agent struct {
	id, parent, state, model string
	until, last              *int64
	created                  bool
	evidence                 []string
}
type projectBinding struct {
	id       string
	evidence []string
}
type bodyRef struct {
	id      string
	ordinal int
}
type sourceModel struct {
	id, source, model string
	request, response []string
	start, end        *int64
}
type source struct {
	id, client, kind, version, adapter string
	last                               int64
}

// Model owns one immutable snapshot. It is safe to query concurrently after construction.
type Model struct {
	edges      map[string]deliveryEdge
	bodyRefs   map[string]bodyRef
	opts       Options
	tasks      map[string]*task
	records    map[string]store.Record
	owners     map[string]map[string]bool
	identities map[string]*identity
	sources    map[string]*source
	models     map[string]*sourceModel
	projects   map[string]projectBinding
	conflict   map[string]bool
	unlinked   map[string]bool
}

func newModel(opts Options) *Model {
	return &Model{edges: map[string]deliveryEdge{}, projects: map[string]projectBinding{}, bodyRefs: map[string]bodyRef{}, opts: opts, tasks: map[string]*task{}, records: map[string]store.Record{}, owners: map[string]map[string]bool{}, identities: map[string]*identity{}, sources: map[string]*source{}, models: map[string]*sourceModel{}, unlinked: map[string]bool{}, conflict: map[string]bool{}}
}

// New builds a model from records returned by a single store.Records call.
func New(records []store.Record, opts Options) *Model {
	m := newModel(opts)
	rows := make([]store.Record, 0, len(records))
	for _, r := range records {
		e := r.Evidence
		c := client(e.Kind)
		if !allowed(opts.Clients, c) || !allowed(opts.Sources, e.Source) {
			continue
		}
		id := recID(r)
		m.records[id] = r
		rows = append(rows, r)
		s := m.sources[e.Source]
		if s == nil {
			s = &source{id: e.Source, client: c}
			m.sources[e.Source] = s
		}
		if s.last <= e.ObservedMS {
			s.kind = string(e.Kind)
			s.adapter = e.AdapterVersion
			s.version = e.ClientVersion
			s.last = e.ObservedMS
		}
	}
	m.decode(rows)
	return m
}

// NewFacts builds the same view from already attributed fact versions. The caller
// supplies only visible versions and their evidence at its selected point.
func NewFacts(events []FactEvent, opts Options) *Model {
	m := newModel(opts)
	m.reduce(slices.Clone(events))
	return m
}

func client(k collect.Kind) string         { return collect.ClientCode(k) }
func allowed(ids []string, id string) bool { return len(ids) == 0 || slices.Contains(ids, id) }
func key(parts ...string) string {
	h := sha256.New()
	for _, s := range parts {
		_, _ = fmt.Fprintf(h, "%d:%s", len(s), s)
	}
	return hex.EncodeToString(h.Sum(nil))
}
func recID(r store.Record) string { return strconv.FormatInt(r.ID, 10) }
func text(m row, k string) string { s, _ := m[k].(string); return s }
func obj(v any) row               { m, _ := v.(map[string]any); return m }
func arr(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []map[string]any:
		r := make([]any, len(x))
		for i := range x {
			r[i] = x[i]
		}
		return r
	case []string:
		r := make([]any, len(x))
		for i := range x {
			r[i] = x[i]
		}
		return r
	}
	return nil
}
func stringsOf(v any) []string {
	var out []string
	for _, i := range arr(v) {
		if s, ok := i.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
func msTime(p *int64) any {
	if p == nil {
		return nil
	}
	return time.UnixMilli(*p).UTC().Format(time.RFC3339Nano)
}
func maybe(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func uniq(list []string, s string) []string {
	if s != "" && !slices.Contains(list, s) {
		return append(list, s)
	}
	return list
}
func maxTime(a, b *int64) *int64 {
	if a == nil || (b != nil && *b > *a) {
		return b
	}
	return a
}
func minTime(a, b *int64) *int64 {
	if a == nil || (b != nil && *b < *a) {
		return b
	}
	return a
}
func (m *Model) boundary() int64 {
	if !m.opts.Until.IsZero() {
		return m.opts.Until.UnixMilli() - 1
	}
	return 1 << 62
}
func fresh(until *int64, at int64) bool { return until != nil && *until > at }
func (m *Model) interval(t *int64) bool {
	if t == nil {
		return m.opts.From.IsZero() && m.opts.Until.IsZero()
	}
	return (m.opts.From.IsZero() || *t >= m.opts.From.UnixMilli()) && (m.opts.Until.IsZero() || *t < m.opts.Until.UnixMilli())
}
func (m *Model) addIdentity(id, parent, client string, evidence []string) {
	if id == "" {
		return
	}
	i := m.identities[id]
	if i == nil {
		i = &identity{id: id, client: client}
		m.identities[id] = i
	}
	if parent != "" && !i.conflict {
		if i.parent != "" && i.parent != parent {
			i.conflict = true
			i.parent = ""
		} else {
			i.parent = parent
		}
	}
	for _, id := range evidence {
		i.supports = uniq(i.supports, id)
	}
}

func (m *Model) reduce(events []FactEvent) {
	// Call and result observations may be delivered in either order. Establish
	// which scoped attempts actually have a call before attaching any result.
	calls := map[string]bool{}
	for _, e := range events {
		if e.Kind == "call" {
			calls[key(e.Task, e.Attempt)] = true
		}
	}
	groups := map[string][]FactEvent{}
	for _, e := range events {
		if e.Task == "" || !allowed(m.opts.Tasks, e.Task) || !allowed(m.opts.Sources, e.Source) || !allowed(m.opts.Clients, e.Client) {
			continue
		}
		if e.Occurred != nil && !m.opts.Until.IsZero() && *e.Occurred >= m.opts.Until.UnixMilli() {
			continue
		}
		if e.Grade == "" {
			e.Grade = "DIRECT"
		}
		if e.Side == "" {
			e.Side = "LOCAL"
		}
		if e.Kind == "result" && (!calls[key(e.Task, e.Attempt)] || e.Attempt == "") {
			for _, id := range e.Evidence {
				m.unlinked[id] = true
			}
			continue
		}
		groups[e.Task] = append(groups[e.Task], e)
	}
	for id, entries := range groups {
		ordered := orderEvents(entries)
		first := ordered[0]
		t := &task{id: id, session: first.Session, root: first.Root, source: first.Source, client: first.Client, state: "UNKNOWN", grade: "UNKNOWN", agents: map[string]agent{}, attempts: map[string]*attempt{}}
		m.tasks[id] = t
		if pr, ok := m.projects[t.session]; ok {
			t.project = pr.id
			t.projectEvidence = pr.evidence
		}
		// A child can report its activities before the parent's callback confirms
		// its assignment. Resolve membership before replaying the observations.
		for _, e := range ordered {
			if e.Kind == "assignment" && e.Target != "" && e.Target != t.root {
				t.agents[e.Target] = agent{id: e.Target, parent: e.Actor, state: "UNKNOWN"}
			}
		}
		for _, e := range ordered {
			t.last = maxTime(t.last, e.Occurred)
			t.events = append(t.events, e)
			for _, eid := range e.Evidence {
				if m.owners[eid] == nil {
					m.owners[eid] = map[string]bool{}
				}
				m.owners[eid][id] = true
			}
			switch e.Kind {
			case "task":
				if e.State != "" {
					t.state = e.State
					t.grade = e.Grade
					t.stateUntil = e.ValidUntil
					t.stateIDs = slices.Clone(e.Evidence)
				}
			case "gap":
				if t.state == "RUNNING" {
					t.state = "UNKNOWN"
					t.grade = "UNKNOWN"
					t.stateUntil = nil
					t.stateIDs = slices.Clone(e.Evidence)
				}
				for id, a := range t.agents {
					if a.state == "RUNNING" && (e.Actor == "" || e.Actor == id) {
						a.state = "UNKNOWN"
						a.until = nil
						t.agents[id] = a
					}
				}
				for _, a := range t.attempts {
					if a.phase == "RUNNING" && (e.Actor == "" || a.actor == e.Actor) {
						a.phase = "UNKNOWN"
						a.until = nil
					}
				}
			case "assignment":
				if e.Target == "" || e.Target == t.root {
					continue
				}
				a := t.agents[e.Target]
				a.id = e.Target
				a.parent = e.Actor
				a.created = a.created || e.Created
				a.state = firstNon(a.state, "UNKNOWN")
				a.evidence = append(a.evidence, e.Evidence...)
				if e.State != "" {
					a.state = e.State
					a.until = e.ValidUntil
					a.last = e.Occurred
				}
				t.agents[a.id] = a
				m.addIdentity(a.id, a.parent, t.client, e.Evidence)
			case "state", "return":
				if a, ok := t.agents[e.Actor]; ok {
					if e.State != "" {
						a.state = e.State
						a.until = e.ValidUntil
					}
					a.last = maxTime(a.last, e.Occurred)
					a.model = firstNon(e.Model, a.model)
					a.evidence = e.Evidence
					t.agents[e.Actor] = a
				}
			case "plan":
				t.plans = append(t.plans, e)
			case "call", "result", "wait", "observer_bridge", "observer_edge", "observer_result":
				if e.Attempt == "" {
					continue
				}
				a := t.attempts[e.Attempt]
				if a == nil {
					a = &attempt{id: e.Attempt, task: id, result: "UNKNOWN", phase: "UNKNOWN"}
					t.attempts[e.Attempt] = a
				}
				a.events = append(a.events, e)
				for _, sid := range e.Evidence {
					a.supports = uniq(a.supports, sid)
				}
				if e.Kind == "observer_edge" || e.Kind == "observer_bridge" {
					continue
				}
				if e.Kind == "result" || e.Kind == "observer_result" {
					if e.Kind == "result" {
						a.end = minTime(a.end, e.Occurred)
					}
					a.phase = "ENDED"
					if e.Result != "" && e.Result != "UNKNOWN" {
						if a.result != "UNKNOWN" && a.result != e.Result {
							a.resultConflict = true
						}
						if a.resultConflict {
							a.result = "UNKNOWN"
						} else {
							a.result = e.Result
						}
					}
				} else {
					if e.Kind == "call" {
						a.actor = e.Actor
						a.name = e.Tool
						a.kind = firstNon(e.ModuleKind, "TOOL")
						a.server = e.Server
						a.module = e.Module
						a.start = minTime(a.start, e.Occurred)
					}
					if a.phase != "ENDED" {
						a.phase = firstNon(e.Phase, "START_OBSERVED")
						if e.ValidUntil != nil {
							a.until = e.ValidUntil
						}
					}
				}
				if e.Reported != nil {
					a.reported = e.Reported
				}
			}
		}
		if t.state == "RUNNING" && !fresh(t.stateUntil, m.boundary()) {
			t.state = "UNKNOWN"
			t.grade = "UNKNOWN"
		}
	}
}

func firstNon(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func clientKind(name string) (kind, server string) {
	if p := strings.SplitN(name, "__", 3); len(p) == 3 && p[0] == "mcp" {
		return "MCP_TOOL", p[1]
	}
	return "TOOL", ""
}
func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
