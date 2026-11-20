package readmodel

import (
	"errors"
	"slices"
	"sort"
	"strconv"
	"strings"
)

func pageInfo(end, total int) row {
	var after any
	if end < total {
		after = strconv.Itoa(end)
	}
	return row{"endCursor": after, "hasNext": end < total, "total": total}
}
func bounds(p row, n int) (int, int) {
	first := 50
	if p["first"] != nil {
		first = jsonNumber(p["first"])
	}
	start, _ := strconv.Atoi(text(p, "after"))
	start = max(0, min(start, n))
	return start, min(n, start+first)
}
func page(items []row, p row) row {
	i, j := bounds(p, len(items))
	if items == nil {
		items = []row{}
	}
	return row{"items": items[i:j], "page": pageInfo(j, len(items))}
}
func field(purpose string) row {
	return row{"purpose": purpose, "state": "DISABLED", "ordinal": 0, "grade": "UNKNOWN", "reason": "metadata_only"}
}
func taskRow(t *task) row {
	return row{"id": t.id, "sessionId": t.session, "client": t.client, "title": field("title"), "state": t.state, "stateGrade": t.grade, "stateEvidenceIds": t.stateIDs, "stateValidUntil": msTime(t.stateUntil), "projectId": maybe(t.project), "projectEvidenceIds": t.projectEvidence, "lastAt": msTime(t.last), "lastActivityKind": t.events[len(t.events)-1].Kind}
}
func (m *Model) agentRow(a agent) row {
	return row{"id": a.id, "identityId": a.id, "modelId": maybe(a.model), "modelGrade": conditional(a.model != "", "DIRECT", "UNKNOWN"), "parentId": maybe(a.parent), "state": m.agentState(a), "lastEvidenceAt": msTime(a.last), "endedAt": conditional(a.state == "ENDED", msTime(a.last), nil)}
}
func conditional[T any](test bool, a, b T) T {
	if test {
		return a
	}
	return b
}
func (m *Model) attemptRow(a *attempt) row {
	phase := m.phase(a)
	outcome := a.result
	if outcome == "UNKNOWN" && phase == "RUNNING" {
		outcome = phase
	}
	return row{"id": a.id, "taskId": a.task, "agentId": maybe(a.actor), "moduleId": maybe(a.module), "outcome": outcome, "phase": phase, "result": a.result, "started": msTime(a.start), "ended": msTime(a.end)}
}
func (m *Model) activities(events []FactEvent, p row) row {
	items := make([]row, 0, len(events))
	for _, e := range events {
		if !m.interval(e.Occurred) {
			continue
		}
		payload := row{"__typename": "StateChange", "code": firstNon(e.State, e.Kind)}
		if e.Attempt != "" {
			payload = row{"__typename": "AttemptChange", "attemptId": e.Attempt, "moduleId": maybe(e.Module), "phase": firstNon(e.Phase, conditional(e.Kind == "result" || e.Kind == "observer_result", "ENDED", "START_OBSERVED")), "result": firstNon(e.Result, "UNKNOWN"), "outcome": firstNon(e.Result, "UNKNOWN")}
		}
		v := row{"id": e.ID, "kind": e.Kind, "at": msTime(e.Occurred), "grade": e.Grade, "agentId": maybe(e.Actor), "attemptId": maybe(e.Attempt), "moduleId": maybe(e.Module), "evidenceIds": e.Evidence, "payload": payload}
		if e.Kind == "assignment" {
			v["identityId"] = e.Target
			v["assignmentId"] = e.ID
			payload = row{"__typename": "MissionChange", "mission": field("mission")}
			m.addMission(payload, e)
			v["payload"] = payload
		}
		if e.Model != "" {
			v["payload"] = row{"__typename": "ModelChange", "model": e.Model, "grade": e.Grade}
		}
		items = append(items, v)
	}
	return page(items, p)
}
func planRow(e FactEvent) row {
	steps := make([]row, len(e.Steps))
	for i, code := range e.Steps {
		steps[i] = row{"key": key(e.ID, strconv.Itoa(i)), "ordinal": i, "title": field("step_title"), "state": code, "grade": e.Grade, "evidenceIds": e.Evidence}
	}
	return row{"id": e.ID, "at": msTime(e.Occurred), "steps": steps}
}

var errSelection = errors.New("invalid read selection")

// Query returns the SDL data field of the requested read. The transport supplies
// the stamp and verifies signed scopes/cursors before passing the decoded offsets.
func (m *Model) Query(operation string, args map[string]any) (any, error) {
	v, err := m.query(operation, args)
	if data, ok := v.(row); ok {
		nonNullLists(data)
	}
	return v, err
}

// SDL list fields return an empty array for an empty set of observations.
func nonNullLists(r row) {
	for k, v := range r {
		switch a := v.(type) {
		case row:
			nonNullLists(a)
		case []row:
			if a == nil {
				r[k] = []row{}
			} else {
				for _, child := range a {
					nonNullLists(child)
				}
			}
		case []string:
			if a == nil {
				r[k] = []string{}
			}
		}
	}
}
func (m *Model) query(operation string, args map[string]any) (any, error) {
	for k, v := range args {
		if k == "page" || strings.HasSuffix(k, "Page") {
			p := obj(v)
			n := jsonNumber(p["first"])
			if p["first"] != nil && (n <= 0 || n > 500) {
				return nil, errSelection
			}
			if s := text(p, "after"); s != "" {
				i, e := strconv.Atoi(s)
				if e != nil || i < 0 {
					return nil, errSelection
				}
			}
		}
	}
	p := obj(args["page"])
	k := obj(args["key"])
	tid, id := text(k, "taskId"), text(k, "id")
	t := m.tasks[tid]
	if tid != "" && (t == nil || !m.visibleTask(t)) {
		return nil, nil
	}
	switch operation {
	case "overview":
		return m.overview(), nil
	case "tasks":
		var items []row
		for _, id := range sortedKeys(m.tasks) {
			t := m.tasks[id]
			if m.visibleTask(t) && (args["activeTasksOnly"] != true || t.state == "RUNNING" && t.grade == "DIRECT") {
				items = append(items, taskRow(t))
			}
		}
		return page(items, p), nil
	case "task", "plans", "agents", "agent", "stepActivities", "assignment":
		if t == nil {
			return nil, nil
		}
		return m.taskData(operation, t, id, args, p), nil
	case "attempts", "attempt", "modules", "moduleGroups", "moduleGroup", "module":
		return m.callData(operation, tid, id, args, p), nil
	case "sources", "source", "unlinked", "evidence", "sourceModelAttempt", "sourceModelAttempts", "identities", "identity":
		return m.sourceData(operation, args, p), nil
	case "sequence", "sequenceMoment", "sequenceInspector":
		if t == nil {
			return nil, nil
		}
		return m.sequence(operation, t, args)
	default:
		return nil, errSelection
	}
}
func (m *Model) taskData(op string, t *task, id string, args, p row) any {
	switch op {
	case "task":
		var active []row
		if len(t.plans) > 0 {
			for _, s := range planRow(t.plans[len(t.plans)-1])["steps"].([]row) {
				if s["state"] == "ACTIVE" {
					active = append(active, s)
				}
			}
		}
		return row{"rootAgentId": maybe(t.root), "task": taskRow(t), "agents": m.agentsCount([]*task{t}), "currentSteps": active, "planAvailable": len(t.plans) > 0, "activities": m.activities(t.events, p)}
	case "agents":
		var items []row
		for _, k := range sortedKeys(t.agents) {
			a := t.agents[k]
			if m.matchesAgent(a) && (text(args, "state") == "" || text(args, "state") == m.agentState(a)) {
				items = append(items, m.agentRow(a))
			}
		}
		return page(items, p)
	case "agent":
		a, ok := t.agents[id]
		if !ok || !m.matchesAgent(a) {
			return nil
		}
		var events []FactEvent
		for _, e := range t.events {
			if e.Actor == id || e.Target == id {
				events = append(events, e)
			}
		}
		v := row{"agent": m.agentRow(a), "mission": field("mission"), "modelId": maybe(a.model), "activities": m.activities(events, p)}
		for _, e := range events {
			if e.Kind == "assignment" && e.Target == id {
				m.addMission(v, e)
			}
		}
		return v
	case "plans":
		var items []row
		for _, e := range t.plans {
			items = append(items, planRow(e))
		}
		v := page(items, p)
		v["versions"] = v["items"]
		delete(v, "items")
		return v
	case "stepActivities":
		for _, e := range t.plans {
			if e.ID == text(args, "planId") {
				for i := range e.Steps {
					if key(e.ID, strconv.Itoa(i)) == text(args, "stepId") {
						return m.activities([]FactEvent{e}, p)
					}
				}
			}
		}
		return nil
	case "assignment":
		for _, e := range t.events {
			if e.Kind == "assignment" && e.ID == id {
				return m.assignment(e)
			}
		}
	}
	return nil
}
func (m *Model) addMission(v row, e FactEvent) {
	if b, ok := m.bodyRefs[e.ID]; ok {
		v["_missionEvidence"] = b.id
		v["_missionOrdinal"] = b.ordinal
	}
}
func (m *Model) assignment(e FactEvent) row {
	v := row{"id": e.ID, "taskId": e.Task, "agentId": e.Target, "identityId": e.Target, "at": msTime(e.Occurred), "mission": field("mission"), "grade": e.Grade, "evidenceIds": e.Evidence}
	m.addMission(v, e)
	return v
}
func (m *Model) callData(op, tid, id string, args, p row) any {
	taskFilter, moduleFilter := text(obj(args["task"]), "taskId"), text(obj(args["module"]), "id")
	if k := text(obj(args["module"]), "taskId"); k != "" {
		if taskFilter != "" && k != taskFilter {
			return nil
		}
		taskFilter = k
	}
	var items []row
	modules := map[string]row{}
	counts := map[string]int{}
	for _, k := range sortedKeys(m.tasks) {
		t := m.tasks[k]
		if !m.visibleTask(t) || tid != "" && tid != k || taskFilter != "" && taskFilter != k {
			continue
		}
		for _, i := range sortedKeys(t.attempts) {
			a := t.attempts[i]
			if !m.matches(a, m.opts.Sample) {
				continue
			}
			if moduleFilter != "" && moduleFilter != a.module && moduleFilter != a.server {
				continue
			}
			if op == "attempt" && id == a.id {
				return m.attemptDetail(a, p)
			}
			if a.module != "" {
				counts[a.module]++
				modules[a.module] = row{"id": a.module, "taskId": k, "kind": a.kind, "name": maybe(a.name), "parentId": maybe(a.server)}
			}
			if a.server != "" {
				counts[a.server]++
				modules[a.server] = row{"id": a.server, "taskId": k, "kind": "MCP_SERVER"}
			}
			if op == "attempts" || op == "module" && (a.module == id || a.server == id) {
				items = append(items, m.attemptRow(a))
			}
		}
	}
	switch op {
	case "attempt":
		return nil
	case "attempts":
		return page(items, p)
	}
	for k, v := range modules {
		v["count"] = m.count(counts[k], Sample{Metric: "ATTEMPTS", TaskID: text(v, "taskId"), ModuleID: k})
	}
	if op == "module" {
		if modules[id] == nil {
			return nil
		}
		var children []row
		for _, k := range sortedKeys(modules) {
			if modules[k]["parentId"] == id {
				children = append(children, modules[k])
			}
		}
		return row{"module": modules[id], "children": page(children, obj(args["childrenPage"])), "attempts": page(items, obj(args["attemptsPage"]))}
	}
	var groups []row
	for _, k := range sortedKeys(modules) {
		v := modules[k]
		if kind := text(args, "kind"); kind != "" && kind != v["kind"] {
			continue
		}
		if op == "modules" {
			items = append(items, v)
			continue
		}
		g := row{"id": k, "kind": v["kind"], "name": v["name"], "identityGrade": "DIRECT", "instances": 1, "measure": v["count"]}
		if op == "moduleGroup" && k == text(args, "groupId") {
			return row{"group": g, "instances": page([]row{v}, p)}
		}
		groups = append(groups, g)
	}
	if op == "moduleGroup" {
		return nil
	}
	if op == "modules" {
		return page(items, p)
	}
	return page(groups, p)
}
func (m *Model) attemptDetail(a *attempt, p row) row {
	var edges, ds []row
	for _, e := range a.events {
		if e.Action == "" {
			continue
		}
		edge := row{"actor": "AGENT", "action": conditional(e.Kind == "result", "RECEIVE", "SEND"), "direction": conditional(e.Kind == "result", "TOOL_RESULT", "TOOL_REQUEST"), "grade": e.Grade, "at": msTime(e.Occurred), "observer": "CLIENT", "evidenceIds": e.Evidence}
		if obs, ok := m.edges[e.ID]; ok {
			edge["actor"] = obs.actor
			edge["action"] = obs.action
			edge["direction"] = obs.direction
			edge["observer"] = obs.observer
		}
		if ref, ok := m.bodyRefs[e.ID]; ok && len(e.Evidence) == 1 && e.Evidence[0] == ref.id {
			edge["_bodyOrdinal"] = ref.ordinal
		}
		edges = append(edges, edge)
	}
	if a.kind == "MCP_TOOL" {
		for _, seg := range []deliveryEdge{{"TOOL", "RECEIVE", "TOOL_REQUEST", "UNKNOWN"}, {"TOOL", "RETURN", "TOOL_RESULT", "UNKNOWN"}} {
			if !slices.ContainsFunc(edges, func(r row) bool {
				return r["actor"] == seg.actor && r["action"] == seg.action && r["direction"] == seg.direction
			}) {
				edges = append(edges, row{"actor": seg.actor, "action": seg.action, "direction": seg.direction, "observer": seg.observer, "grade": "UNKNOWN", "reason": "server_observation_missing", "evidenceIds": []string{}})
			}
		}
	}
	for _, basis := range []string{"REPORTED", "OBSERVED_INTERVAL"} {
		if v, ok := duration(a, basis); ok {
			ds = append(ds, row{"basis": basis, "ms": v, "evidenceIds": a.supports})
		}
	}
	return row{"attempt": m.attemptRow(a), "edges": edges, "request": []row{field("request")}, "response": []row{field("response")}, "conversation": []row{}, "linkedNextAttemptIds": []string{}, "durations": ds, "evidence": m.evidencePage(a.supports, p)}
}
func (m *Model) evidenceRow(id string) row {
	r, ok := m.records[id]
	if !ok {
		return nil
	}
	e := r.Evidence
	return row{"id": id, "sourceId": e.Source, "observer": strings.ToUpper(e.Observer), "occurred": msTime(e.OccurredMS), "observed": msTime(&e.ObservedMS), "recordKind": firstNon(text(e.Metadata, "type"), firstNon(text(e.Metadata, "event.name"), text(e.Metadata, "method"))), "grade": "DIRECT", "link": conditional(m.conflict[id], "CONFLICT", conditional(m.unlinked[id], "UNLINKED", "MATCHED")), "adapterVersion": maybe(e.AdapterVersion), "streamScopeId": e.Stream, "familyScopeId": e.Profile}
}
func (m *Model) evidenceInInterval(id string) bool {
	e, ok := m.records[id]
	if !ok {
		return false
	}
	at := e.Evidence.OccurredMS
	if at == nil {
		at = &e.Evidence.ObservedMS
	}
	return m.interval(at)
}
func (m *Model) evidencePage(ids []string, p row) row {
	var values []row
	for _, sid := range ids {
		if r := m.evidenceRow(sid); r != nil {
			values = append(values, r)
		}
	}
	return page(values, p)
}
func (m *Model) sourceData(op string, args, p row) any {
	sid, id := text(args, "sourceId"), text(args, "id")
	sourceOnly := len(m.opts.Tasks) == 0 && m.opts.Sample.TaskID == "" && m.opts.Sample.Metric == "" || len(m.opts.Tasks) == 0 && m.opts.Sample.TaskID == "" && m.opts.Sample.Metric == "ALL"
	if op == "evidence" {
		eid := text(args, "evidenceId")
		v := m.evidenceRow(eid)
		if v == nil {
			return nil
		}
		if sourceOnly && len(m.owners[eid]) == 0 && !m.evidenceInInterval(eid) {
			return nil
		}
		if !sourceOnly {
			ok := false
			for owner := range m.owners[eid] {
				if t := m.tasks[owner]; t != nil && m.visibleTask(t) {
					for _, a := range t.attempts {
						if m.matches(a, m.opts.Sample) && slices.Contains(a.supports, eid) {
							ok = true
						}
					}
					if m.opts.Sample.Metric == "" || m.opts.Sample.Metric == "ALL" {
						ok = true
					}
				}
			}
			if !ok {
				return nil
			}
		}
		return row{"evidence": v, "fields": []row{field("body")}, "candidates": 0}
	}
	if op == "identities" || op == "identity" {
		var items []row
		for _, k := range sortedKeys(m.identities) {
			i := m.identities[k]
			var assigned []row
			for _, t := range m.tasks {
				if !m.visibleTask(t) {
					continue
				}
				for _, e := range t.events {
					if e.Kind == "assignment" && e.Target == k {
						assigned = append(assigned, m.assignment(e))
					}
				}
			}
			attr := conditional(len(assigned) > 0, "CONFIRMED", "UNASSIGNED")
			if i.conflict {
				attr = "AMBIGUOUS"
			}
			if !sourceOnly && len(assigned) == 0 || text(args, "attribution") != "" && text(args, "attribution") != attr {
				continue
			}
			v := row{"id": k, "client": i.client, "parentId": maybe(i.parent), "parentGrade": conditional(i.parent != "", "DIRECT", "UNKNOWN"), "attribution": attr}
			items = append(items, v)
			if op == "identity" && k == id {
				sort.Slice(assigned, func(i, j int) bool { return text(assigned[i], "id") < text(assigned[j], "id") })
				return row{"identity": v, "assignments": page(assigned, obj(args["assignmentsPage"])), "evidence": m.evidencePage(i.supports, obj(args["evidencePage"]))}
			}
		}
		if op == "identities" {
			return page(items, p)
		}
		return nil
	}
	if !sourceOnly {
		return nil
	}
	if strings.HasPrefix(op, "sourceModel") && (len(m.opts.Sources) != 1 || m.opts.Sources[0] != sid) {
		return nil
	}
	var items []row
	if op == "sources" || op == "source" {
		for _, k := range sortedKeys(m.sources) {
			s := m.sources[k]
			v := row{"id": k, "client": s.client, "kind": s.kind, "clientVersion": maybe(s.version), "adapterVersion": s.adapter, "permission": "OBSERVED", "lastAt": msTime(&s.last)}
			if op == "source" && sid == k {
				count := 0
				for id := range m.unlinked {
					if m.records[id].Evidence.Source == sid && m.evidenceInInterval(id) {
						count++
					}
				}
				return row{"source": v, "capabilities": []row{{"code": "metadata", "state": "AVAILABLE"}}, "gaps": []row{}, "unlinkedCount": count, "consent": false, "enabled": true, "pickerRef": k, "retentionDays": 7}
			}
			items = append(items, v)
		}
		if op == "source" {
			return nil
		}
		return page(items, p)
	}
	if op == "unlinked" {
		var ids []string
		for _, id := range sortedKeys(m.unlinked) {
			if m.records[id].Evidence.Source == sid && m.evidenceInInterval(id) {
				ids = append(ids, id)
			}
		}
		return m.evidencePage(ids, p)
	}
	for _, k := range sortedKeys(m.models) {
		a := m.models[k]
		if a.source != sid {
			continue
		}
		refs := append(slices.Clone(a.request), a.response...)
		if !slices.ContainsFunc(refs, m.evidenceInInterval) {
			continue
		}
		v := row{"id": k, "sourceId": sid, "modelId": maybe(a.model), "modelGrade": conditional(a.model != "", "DIRECT", "UNKNOWN"), "modelEvidenceIds": refs, "phase": conditional(len(a.response) > 0, "ENDED", "START_OBSERVED"), "result": conditional(len(a.response) > 0, "SUCCESS", "UNKNOWN"), "started": msTime(a.start), "ended": msTime(a.end)}
		if op == "sourceModelAttempt" && id == k {
			var edges []row
			for _, eid := range refs {
				r := m.records[eid]
				req := slices.Contains(a.request, eid)
				edges = append(edges, row{"actor": "CLIENT", "action": conditional(req, "SEND", "RECEIVE"), "direction": conditional(req, "MODEL_REQUEST", "MODEL_RESPONSE"), "grade": "DIRECT", "at": msTime(r.Evidence.OccurredMS), "observer": "CLIENT", "evidenceIds": []string{eid}})
			}
			return row{"attempt": v, "edges": edges, "request": []row{field("request")}, "response": []row{field("response")}, "conversation": []row{}, "evidence": m.evidencePage(refs, obj(args["evidencePage"]))}
		}
		items = append(items, v)
	}
	if op == "sourceModelAttempt" {
		return nil
	}
	return page(items, p)
}
