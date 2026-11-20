package readmodel

import (
	"container/heap"
	"slices"
	"sort"
)

// Each clock chooses its earliest ready event. Only stable IDs are compared across clocks.
type queue struct {
	entries []int
	less    func(int, int) bool
}

func (q queue) Len() int           { return len(q.entries) }
func (q queue) Less(i, j int) bool { return q.less(q.entries[i], q.entries[j]) }
func (q queue) Swap(i, j int)      { q.entries[i], q.entries[j] = q.entries[j], q.entries[i] }
func (q *queue) Push(x any)        { q.entries = append(q.entries, x.(int)) }
func (q *queue) Pop() any {
	n := len(q.entries) - 1
	v := q.entries[n]
	q.entries = q.entries[:n]
	return v
}

func orderEvents(events []FactEvent) []FactEvent {
	n := len(events)
	byID := make(map[string]int, n)
	streams := map[string][]int{}
	calls := map[string]int{}
	for i, e := range events {
		byID[e.ID] = i
		if e.Ordered {
			streams[e.Stream] = append(streams[e.Stream], i)
		}
		if e.Kind == "call" && e.Attempt != "" {
			calls[e.Attempt] = i
		}
	}
	for _, ids := range streams {
		sort.Slice(ids, func(i, j int) bool { return events[ids[i]].Position < events[ids[j]].Position })
		for j := 1; j < len(ids); j++ {
			p, c := events[ids[j-1]], &events[ids[j]]
			if p.Position == c.Position || j > 1 && events[ids[j-2]].Position == p.Position || j+1 < len(ids) && events[ids[j+1]].Position == c.Position {
				continue
			}
			c.Parents = append(slices.Clone(c.Parents), Relation{Kind: "STREAM_ORDER", From: p.ID})
		}
	}
	next := make([][]int, n)
	deps := make([]int, n)
	for i := range events {
		e := &events[i]
		if e.Kind == "result" {
			if c, ok := calls[e.Attempt]; ok {
				e.Parents = append(slices.Clone(e.Parents), Relation{Kind: "CALL_RESULT", From: events[c].ID})
			}
		}
		var valid []Relation
		seen := map[Relation]bool{}
		inputs := map[string]bool{}
		for _, r := range e.Parents {
			j, ok := byID[r.From]
			if !ok || j == i || seen[r] {
				continue
			}
			if r.Kind == "JOIN_INPUT" {
				p := events[j]
				if e.Action != "JOIN" || p.Kind != "return" || p.Target != e.Actor || inputs[p.Actor] {
					continue
				}
				inputs[p.Actor] = true
			}
			seen[r] = true
			valid = append(valid, r)
		}
		if e.Action == "JOIN" && len(inputs) < 2 {
			e.Action = "STATE"
			valid = slices.DeleteFunc(valid, func(r Relation) bool { return r.Kind == "JOIN_INPUT" })
		}
		e.Parents = valid
		for _, r := range valid {
			if r.Kind != "OVERLAP" {
				next[byID[r.From]] = append(next[byID[r.From]], i)
				deps[i]++
			}
		}
	}
	clockBy := make([]string, n)
	local := map[string]*queue{}
	reps := &queue{less: func(i, j int) bool { return events[i].ID < events[j].ID }}
	push := func(i int) {
		e := events[i]
		domain := e.Clock
		if domain == "" || e.Occurred == nil {
			domain = "unknown/" + e.ID
		}
		clockBy[i] = domain
		q := local[domain]
		if q == nil {
			q = &queue{less: func(i, j int) bool {
				a, b := events[i], events[j]
				if a.Occurred != nil && b.Occurred != nil && *a.Occurred != *b.Occurred {
					return *a.Occurred < *b.Occurred
				}
				return a.ID < b.ID
			}}
			local[domain] = q
		}
		heap.Push(q, i)
		if q.entries[0] == i {
			heap.Push(reps, i)
		}
	}
	for i := range events {
		if deps[i] == 0 {
			push(i)
		}
	}
	out := make([]FactEvent, 0, n)
	used := make([]bool, n)
	for reps.Len() > 0 {
		i := heap.Pop(reps).(int)
		q := local[clockBy[i]]
		if used[i] || q.Len() == 0 || q.entries[0] != i {
			continue
		}
		heap.Pop(q)
		used[i] = true
		out = append(out, events[i])
		if q.Len() > 0 {
			heap.Push(reps, q.entries[0])
		}
		for _, j := range next[i] {
			deps[j]--
			if deps[j] == 0 {
				push(j)
			}
		}
	}
	if len(out) < n {
		var conflict []FactEvent
		for i, e := range events {
			if !used[i] {
				e.Parents = nil
				e.Grade = "UNKNOWN"
				conflict = append(conflict, e)
			}
		}
		sort.Slice(conflict, func(i, j int) bool { return conflict[i].ID < conflict[j].ID })
		out = append(out, conflict...)
	}
	return out
}
