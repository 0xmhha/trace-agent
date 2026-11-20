package readmodel

import (
	"slices"
	"strconv"
)

type laneState struct {
	state, activity, grade string
	at, until              *int64
	refs                   []string
}
type waiting struct {
	actor, target string
	until         *int64
	refs          []string
}
type sequenceState struct {
	child     map[string]bool
	states    map[string]laneState
	waits     map[string]waiting
	last      map[string]FactEvent
	missions  map[string]FactEvent
	lastEvent FactEvent
}

func initialSequence() *sequenceState {
	return &sequenceState{missions: map[string]FactEvent{}, child: map[string]bool{}, states: map[string]laneState{}, waits: map[string]waiting{}, last: map[string]FactEvent{}}
}
func (s *sequenceState) apply(e FactEvent) {
	if e.Kind == "assignment" {
		s.child[e.Target] = true
		s.missions[e.Target] = e
	}
	lane := e.Actor
	if e.Kind == "assignment" {
		lane = e.Target
	}
	if e.State != "" {
		s.states[lane] = laneState{e.State, conditional(e.State == "ENDED", "ENDED", "ACTIVE"), e.Grade, e.Occurred, e.ValidUntil, e.Evidence}
	}
	if e.Kind == "gap" {
		for id, w := range s.waits {
			if e.Actor == "" || e.Actor == w.actor {
				delete(s.waits, id)
			}
		}
		for id, v := range s.states {
			if v.state != "ENDED" && (e.Actor == "" || e.Actor == id) {
				delete(s.states, id)
			}
		}
	}
	if e.Kind == "result" || e.Kind == "observer_result" {
		delete(s.waits, e.Attempt)
	}
	if (e.Kind == "wait" || e.Kind == "call") && e.Phase == "RUNNING" && e.ValidUntil != nil && e.Attempt != "" {
		s.waits[e.Attempt] = waiting{e.Actor, e.Target, e.ValidUntil, e.Evidence}
	}
	if e.State == "ENDED" {
		for id, w := range s.waits {
			if w.actor == lane {
				delete(s.waits, id)
			}
		}
	}
	if e.Actor != "" {
		s.last[e.Actor] = e
	}
	if e.Target != "" {
		s.last[e.Target] = e
	}
	s.lastEvent = e
}
func (s *sequenceState) status(id string, at int64) row {
	st := s.states[id]
	state := firstNon(st.state, "UNKNOWN")
	act := firstNon(st.activity, "UNKNOWN")
	grade := firstNon(st.grade, "UNKNOWN")
	if state == "RUNNING" && !fresh(st.until, at) {
		state = "UNKNOWN"
		act = "UNKNOWN"
		grade = "UNKNOWN"
	}
	if state == "RUNNING" {
		for _, w := range s.waits {
			if w.actor == id && fresh(w.until, at) {
				act = "WAITING_TOOL"
				break
			}
		}
	}
	return row{"state": state, "activity": act, "at": msTime(st.at), "grade": grade, "evidenceIds": st.refs}
}
func (s *sequenceState) metrics(m *Model, at int64) row {
	children, running, waits := len(s.child), 0, 0
	for id := range s.child {
		if s.status(id, at)["state"] == "RUNNING" {
			running++
		}
	}
	for _, w := range s.waits {
		if fresh(w.until, at) {
			waits++
		}
	}
	return row{"childAgents": m.count(children, Sample{}), "runningChildAgents": m.count(running, Sample{}), "waitingAttempts": m.count(waits, Sample{}), "lastEventId": maybe(s.lastEvent.ID), "lastEventAt": msTime(s.lastEvent.Occurred)}
}
func sequenceRow(e FactEvent) row {
	from, to := e.Actor, e.Target
	if e.Kind == "result" || e.Kind == "observer_result" {
		from = e.Module
		to = e.Actor
	}
	v := row{"id": e.ID, "action": firstNon(e.Action, "STATE"), "fromLaneId": maybe(from), "toLaneId": maybe(to), "observedSide": firstNon(e.Side, "LOCAL"), "occurred": msTime(e.Occurred), "observed": msTime(&e.Observed), "grade": firstNon(e.Grade, "DIRECT"), "attemptId": maybe(e.Attempt), "evidenceIds": e.Evidence}
	if e.Kind == "assignment" {
		v["assignmentId"] = e.ID
	}
	if e.State != "" {
		v["stateAfter"] = row{"state": e.State, "activity": conditional(e.State == "ENDED", "ENDED", "ACTIVE"), "grade": e.Grade, "at": msTime(e.Occurred), "evidenceIds": e.Evidence}
	}
	if e.Attempt != "" {
		v["attemptAfter"] = row{"attemptId": e.Attempt, "moduleId": maybe(e.Module), "phase": firstNon(e.Phase, conditional(e.Kind == "result" || e.Kind == "observer_result", "ENDED", "START_OBSERVED")), "result": firstNon(e.Result, "UNKNOWN"), "outcome": firstNon(e.Result, "UNKNOWN")}
	}
	return v
}
func (m *Model) sequence(op string, t *task, args row) (any, error) {
	s := initialSequence()
	agents := map[string]bool{t.root: true}
	parents := map[string]string{}
	kinds := map[string]string{t.root: "AGENT"}
	type item struct {
		event FactEvent
		index int
	}
	var timeline []item
	indices := map[string]int{}
	for i, e := range t.events {
		if e.Kind == "assignment" {
			agents[e.Target] = true
			parents[e.Target] = e.Actor
			kinds[e.Target] = "AGENT"
		}
		if e.Actor != "" {
			kinds[e.Actor] = "AGENT"
		}
		if e.Module != "" {
			kinds[e.Module] = conditional(e.ModuleKind == "MCP_TOOL", "TOOL", firstNon(e.ModuleKind, "TOOL"))
		}
		if e.Target != "" && kinds[e.Target] == "" {
			kinds[e.Target] = "AGENT"
		}
		// Untimed relationships are shown in the tree. They do not create a timeline row.
		if e.Occurred == nil || !m.interval(e.Occurred) || e.Action == "" {
			continue
		}
		if focus := text(args, "focusAgentId"); focus != "" && e.Actor != focus && e.Target != focus {
			continue
		}
		indices[e.ID] = i
		timeline = append(timeline, item{e, i})
	}
	if op != "sequence" {
		i, ok := indices[text(args, "eventId")]
		if !ok {
			return nil, nil
		}
		for _, e := range t.events[:i+1] {
			if e.Occurred != nil {
				s.apply(e)
			}
		}
		e := t.events[i]
		at := *e.Occurred
		if op == "sequenceMoment" {
			var states []row
			for _, lane := range stringsOf(args["laneIds"]) {
				if !agents[lane] {
					continue
				}
				states = append(states, row{"laneId": lane, "status": s.status(lane, at)})
			}
			return row{"taskId": t.id, "eventId": e.ID, "lanes": states, "metrics": s.metrics(m, at)}, nil
		}
		lane := text(args, "laneId")
		if kinds[lane] == "" {
			return nil, nil
		}
		var waits []row
		for _, id := range sortedKeys(s.waits) {
			w := s.waits[id]
			if w.actor == lane && fresh(w.until, at) {
				waits = append(waits, row{"laneId": maybe(w.target), "attemptId": id, "grade": "DIRECT", "evidenceIds": w.refs})
			}
		}
		var latest, status any
		if last, ok := s.last[lane]; ok {
			latest = sequenceRow(last)
		}
		if agents[lane] {
			status = s.status(lane, at)
		}
		v := row{"laneId": lane, "eventId": e.ID, "status": status, "mission": field("mission"), "latestEvent": latest, "waits": page(waits, obj(args["waitsPage"]))}
		if mission, ok := s.missions[lane]; ok {
			m.addMission(v, mission)
		}
		return v, nil
	}
	p := obj(args["page"])
	start, end := bounds(p, len(timeline))
	first := end - start
	if text(args, "window") == "LATEST" && text(p, "after") == "" {
		end = len(timeline)
		start = max(0, end-first)
	}
	beforeIndex := 0
	if len(timeline) > 0 && start < len(timeline) {
		beforeIndex = timeline[start].index
	}
	for _, e := range t.events[:beforeIndex] {
		if e.Occurred != nil {
			s.apply(e)
		}
	}
	beforeAt := m.boundary()
	if start < len(timeline) {
		beforeAt = *timeline[start].event.Occurred
	}
	before := s.metrics(m, beforeAt)
	beforeLanes := map[string]row{}
	for id := range agents {
		beforeLanes[id] = s.status(id, beforeAt)
	}
	for _, e := range t.events[beforeIndex:] {
		if e.Occurred != nil {
			s.apply(e)
		}
	}
	lastAt := m.boundary()
	if m.opts.Until.IsZero() && s.lastEvent.Occurred != nil {
		lastAt = *s.lastEvent.Occurred
	}
	var lanes, events, relations []row
	visible := map[string]bool{}
	for _, it := range timeline[start:end] {
		events = append(events, sequenceRow(it.event))
		visible[it.event.ID] = true
	}
	for _, it := range timeline {
		e := it.event
		for _, r := range e.Parents {
			_, ok := indices[r.From]
			if ok && (visible[e.ID] || visible[r.From]) {
				relations = append(relations, row{"kind": r.Kind, "fromEventId": r.From, "toEventId": e.ID, "fromOnPage": visible[r.From], "toOnPage": visible[e.ID], "grade": e.Grade, "evidenceIds": e.Evidence})
			}
		}
	}
	for _, id := range sortedKeys(kinds) {
		if id == "" {
			continue
		}
		v := row{"id": id, "kind": kinds[id], "parentLaneId": maybe(parents[id]), "parentGrade": conditional(parents[id] != "", "DIRECT", "UNKNOWN")}
		if agents[id] {
			v["agentId"] = id
			v["identityId"] = id
			v["beforePage"] = beforeLanes[id]
			v["latest"] = s.status(id, lastAt)
		} else {
			v["moduleId"] = id
		}
		lanes = append(lanes, v)
	}
	rp := page(relations, obj(args["relationsPage"]))
	var previous any
	size := 50
	if p["first"] != nil {
		size = jsonNumber(p["first"])
	}
	if start > 0 {
		previous = strconv.Itoa(max(0, start-size))
	}
	return row{"taskId": t.id, "previousPage": previous, "metricsBeforePage": before, "latestMetrics": s.metrics(m, lastAt), "lanes": lanes, "events": events, "relations": rp["items"], "relationPage": rp["page"], "page": pageInfo(end, len(timeline))}, nil
}

// Entry chooses an initial sequence task. A stale explicit link opens the selector.
func (m *Model) Entry(explicit, last string) (string, []string) {
	for _, id := range []string{explicit, last} {
		if id == "" {
			continue
		}
		if t := m.tasks[id]; t != nil && m.visibleTask(t) {
			return id, nil
		}
		if id == explicit {
			break
		}
	}
	var ids []string
	for _, t := range m.tasks {
		if m.visibleTask(t) && t.state == "RUNNING" && t.grade == "DIRECT" {
			ids = append(ids, t.id)
		}
	}
	slices.Sort(ids)
	if len(ids) == 1 && explicit == "" {
		return ids[0], nil
	}
	return "", ids
}
