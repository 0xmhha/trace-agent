package readmodel

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

type direction struct{ session, turn, actor string }
type decoder struct {
	m      *Model
	events []FactEvent
	roll   map[string]direction
}

func streamID(e collect.Evidence) string { return key(e.Source, e.Generation, e.Stream) }
func (d *decoder) event(r store.Record, dir direction, kind string, n int) FactEvent {
	e := r.Evidence
	root := collect.SessionKey(e.Profile, dir.session)
	return FactEvent{ID: key(e.Source, e.Stream, e.Generation, e.RecordKey, e.VersionKey, strconv.Itoa(n), kind), Task: collect.TaskKey(e.Profile, dir.session, dir.turn), Session: root, Root: root, Actor: firstNon(dir.actor, root), Source: e.Source, Client: client(e.Kind), Kind: kind, Grade: "DIRECT", Side: "LOCAL", Occurred: e.OccurredMS, Observed: e.ObservedMS, Evidence: []string{recID(r)}, Stream: streamID(e), Clock: e.Source, Position: e.Position, Ordered: e.Kind == collect.Rollout || e.Kind == collect.Transcript}
}
func (d *decoder) emit(e FactEvent) { d.events = append(d.events, e) }
func (d *decoder) tool(r store.Record, dir direction, index int, id, name, server, kind string) FactEvent {
	e := d.event(r, dir, kind, index)
	d.m.bodyRefs[e.ID] = bodyRef{recID(r), index}
	if id != "" {
		e.Attempt = key(e.Task, streamID(r.Evidence), id)
	}
	e.Tool = name
	e.ModuleKind, e.Server = clientKind(name)
	if server != "" {
		e.ModuleKind = "MCP_TOOL"
		e.Server = server
	}
	if e.Server != "" {
		e.Server = key(e.Task, "server", e.Server)
	}
	if name != "" {
		e.Module = key(e.Task, e.ModuleKind, e.Server, name)
	}
	if kind == "call" {
		e.Action = "CALL"
		e.Side = "SENDER"
		e.Target = e.Module
	} else {
		e.Action = "RETURN"
		e.Side = "RECEIVER"
	}
	return e
}
func (m *Model) decode(records []store.Record) {
	sort.SliceStable(records, func(i, j int) bool {
		a, b := records[i], records[j]
		x, y := a.Evidence, b.Evidence
		if x.Source != y.Source {
			return x.Source < y.Source
		}
		if x.Stream != y.Stream {
			return x.Stream < y.Stream
		}
		if x.Generation != y.Generation {
			return x.Generation < y.Generation
		}
		if x.Position != y.Position {
			return x.Position < y.Position
		}
		return a.ID < b.ID
	})
	d := decoder{m: m, roll: map[string]direction{}}
	var cl []store.Record
	for _, r := range records {
		if at := r.Evidence.OccurredMS; at != nil && !m.opts.Until.IsZero() && *at >= m.opts.Until.UnixMilli() {
			continue
		}
		switch r.Evidence.Kind {
		case collect.Transcript:
			cl = append(cl, r)
		case collect.Rollout:
			d.rollout(r)
		case collect.AppServer:
			d.app(r)
		case collect.CodexTelemetry:
			d.m.unlinked[recID(r)] = true
		case collect.Telemetry:
			d.otel(r)
		}
	}
	d.claude(cl)
	d.resolveCalls()
	d.observedEdges(records)
	// A response alone is unlinked. Key equality cannot create a request.
	for k, a := range m.models {
		if len(a.request) == 0 {
			for _, id := range a.response {
				m.unlinked[id] = true
			}
			delete(m.models, k)
		}
	}
	// A materialized version supersedes the generic interpretation of the same fact.
	var versions map[string]FactEvent
	for _, r := range records {
		for _, f := range r.Facts {
			if versions == nil {
				versions = map[string]FactEvent{}
			}
			versions[f.ID] = f
		}
	}
	if len(versions) > 0 {
		merged := make([]FactEvent, 0, len(versions)+len(d.events))
		for _, e := range d.events {
			if _, ok := versions[e.ID]; !ok {
				merged = append(merged, e)
			}
		}
		for _, id := range sortedKeys(versions) {
			merged = append(merged, versions[id])
		}
		d.events = merged
	}
	m.reduce(d.events)
}

// resolveCalls accepts only unique native calls. Reused call IDs in one trace
// are ambiguous, including results and assignments linked to those calls.
func (d *decoder) resolveCalls() {
	seen := map[string]string{}
	descriptions := map[string]FactEvent{}
	conflict := map[string]bool{}
	waitCalls := map[string]bool{}
	for _, e := range d.events {
		if e.Kind != "call" || e.Attempt == "" || len(e.Evidence) == 0 {
			continue
		}
		if old, ok := descriptions[e.Attempt]; !ok || old.Action == "" || e.Action != "" {
			descriptions[e.Attempt] = e
		}
		r := d.m.records[e.Evidence[0]]
		if r.Evidence.Kind == collect.AppServer {
			continue
		}
		if id, ok := seen[e.Attempt]; ok && id != e.ID {
			conflict[e.Attempt] = true
		}
		seen[e.Attempt] = e.ID
		if e.Tool == "wait_agent" {
			waitCalls[e.Attempt] = true
		}
	}
	accepted := make([]FactEvent, 0, len(d.events))
	for _, e := range d.events {
		if e.Kind == "call" && e.Action == "" && descriptions[e.Attempt].Action != "" {
			continue
		}
		if conflict[e.Attempt] {
			for _, id := range e.Evidence {
				d.m.unlinked[id] = true
				d.m.conflict[id] = true
			}
			continue
		}
		if call, ok := descriptions[e.Attempt]; ok && e.Kind == "result" && e.Module == "" {
			e.Module = call.Module
			e.ModuleKind = call.ModuleKind
			e.Tool = call.Tool
			e.Server = call.Server
		}
		if e.Kind == "result" && waitCalls[e.Attempt] && len(e.Evidence) > 0 {
			r := d.m.records[e.Evidence[0]]
			var report struct {
				TimedOut bool                         `json:"timed_out"`
				Status   map[string]map[string]string `json:"status"`
			}
			if json.Unmarshal([]byte(text(obj(r.Evidence.Metadata["payload"]), "output")), &report) == nil {
				if report.TimedOut {
					e.Action = "TIMEOUT"
				}
				for _, child := range sortedKeys(report.Status) {
					_, completed := report.Status[child]["completed"]
					_, failed := report.Status[child]["errored"]
					if !completed && !failed {
						continue
					}
					v := e
					v.ID = key(e.ID, "state", child)
					v.Kind = "state"
					v.Action = "STATE"
					v.State = "ENDED"
					v.Actor = collect.SessionKey(r.Evidence.Profile, child)
					v.Attempt = ""
					v.Target = ""
					accepted = append(accepted, v)
				}
			}
		}
		if e.Kind == "result" && len(e.Evidence) > 0 {
			call := descriptions[e.Attempt]
			if len(call.Evidence) > 0 {
				req := d.m.records[call.Evidence[0]].Evidence
				res := d.m.records[e.Evidence[0]].Evidence
				kid := collect.SessionKey(res.Profile, text(obj(res.Metadata["payload"]), "reportedChildId"))
				child := d.m.identities[kid]
				if req.Kind == collect.Rollout && text(obj(req.Metadata["payload"]), "coordination") == "spawn" && child != nil && !child.conflict && child.parent == e.Actor {
					v := e
					v.Kind = "assignment"
					v.ID = key(e.ID, "spawn")
					v.Action = "SPAWN"
					v.Target = kid
					v.Created = true
					v.Attempt = ""
					v.Module = ""
					v.Evidence = append(slicesCopy(e.Evidence), call.Evidence...)
					v.Evidence = append(v.Evidence, child.supports...)
					d.m.bodyRefs[v.ID] = d.m.bodyRefs[call.ID]
					accepted = append(accepted, v)
					e.Result = "SUCCESS"
				}
			}
		}
		accepted = append(accepted, e)
	}
	d.events = accepted
}
func (d *decoder) rollout(r store.Record) {
	e := r.Evidence
	s := streamID(e)
	dir := d.roll[s]
	m := e.Metadata
	p := obj(m["payload"])
	switch text(m, "type") {
	case "session_meta":
		dir = direction{session: text(p, "id")}
		d.roll[s] = dir
		id := collect.SessionKey(e.Profile, dir.session)
		parent := collect.SessionKey(e.Profile, text(p, "parent_thread_id"))
		d.m.addIdentity(id, parent, client(e.Kind), []string{recID(r)})
		if i := d.m.identities[id]; i != nil && p["parent_conflict"] == true {
			i.conflict = true
			i.parent = ""
		}
	case "event_msg":
		if text(p, "turn_id") != "" {
			dir.turn = text(p, "turn_id")
		}
		explicit := text(p, "type")
		v := d.event(r, dir, "task", 0)
		v.Action = "STATE"
		if explicit == "task_complete" {
			v.State = "ENDED"
			dir.turn = ""
		} else {
			v.State = "UNKNOWN"
		}
		d.roll[s] = dir
		d.emit(v)
	case "turn_context":
		if text(p, "turn_id") != "" {
			dir.turn = text(p, "turn_id")
			d.roll[s] = dir
		}
	case "response_item":
		if turn := text(p, "turn_id"); turn != "" {
			dir.turn = turn
		}
		typ := text(p, "type")
		id := text(p, "call_id")
		if typ == "message" {
			v := d.event(r, dir, "message", 0)
			v.Action = "SEND"
			d.emit(v)
			return
		}
		if dir.turn == "" || id == "" {
			d.m.unlinked[recID(r)] = true
			return
		}
		kind := "call"
		if strings.HasSuffix(typ, "_output") {
			kind = "result"
		}
		family := strings.TrimSuffix(typ, "_output")
		v := d.tool(r, dir, 0, family+"/"+id, text(p, "name"), text(p, "mcpServerKey"), kind)
		v.Grade = "INFERRED"
		d.emit(v)
	}
}
func (d *decoder) app(r store.Record) {
	e := r.Evidence
	m := e.Metadata
	if text(m, "method") == "turn/plan/updated" {
		p := obj(m["params"])
		dir := direction{session: text(p, "threadId"), turn: text(p, "turnId")}
		v := d.event(r, dir, "plan", 0)
		v.Action = "STATE"
		for _, s := range arr(p["plan"]) {
			code := map[string]string{"inProgress": "ACTIVE", "pending": "PENDING", "completed": "DONE"}[text(obj(s), "status")]
			v.Steps = append(v.Steps, firstNon(code, "UNKNOWN"))
		}
		d.emit(v)
		return
	}
	dir := direction{session: text(m, "threadId"), turn: text(m, "turnId")}
	typ := text(m, "type")
	status := text(m, "status")
	switch typ {
	case "thread":
		if project := text(m, "projectId"); project != "" {
			id := collect.SessionKey(e.Profile, text(m, "id"))
			// Configuration is revisioned. Only the most recent visible binding
			// supplies the label-independent project ID.
			d.m.projects[id] = projectBinding{key(e.Source, project), []string{recID(r)}}
		}
		d.m.addIdentity(collect.SessionKey(e.Profile, text(m, "id")), collect.SessionKey(e.Profile, text(m, "parentThreadId")), client(e.Kind), []string{recID(r)})
	case "turn":
		v := d.event(r, dir, "task", 0)
		v.Action = "STATE"
		v.State = "UNKNOWN"
		if status == "completed" || status == "failed" || status == "interrupted" {
			v.State = "ENDED"
		}
		d.emit(v)
	case "commandExecution", "mcpToolCall", "dynamicToolCall", "collabAgentToolCall":
		name := firstNon(text(m, "tool"), typ)
		phase, _, _ := strings.Cut(e.VersionKey, "/")
		kind := "call"
		if phase == "completed" {
			kind = "result"
		}
		if sender := text(m, "senderThreadId"); typ == "collabAgentToolCall" && sender != "" {
			dir.actor = collect.SessionKey(e.Profile, sender)
		}
		v := d.tool(r, dir, 0, text(m, "id"), name, "", kind)
		if typ == "mcpToolCall" {
			v.ModuleKind = "MCP_TOOL"
			if server := text(m, "server"); server != "" {
				v.Server = key(v.Task, "server", server)
			}
			if v.Server != "" {
				v.Module = key(v.Task, v.ModuleKind, v.Server, name)
			} else {
				v.Module = key(v.Task, v.Attempt, "unresolved_server", name)
			}
			if kind == "call" {
				v.Target = v.Module
			}
		}
		if kind == "result" {
			switch status {
			case "failed":
				v.Result = "TOOL_ERROR"
			case "cancelled", "declined":
				v.Result = "CANCELLED"
			}
			if result, ok := m["isError"].(bool); ok {
				v.Result = "SUCCESS"
				if result {
					v.Result = "TOOL_ERROR"
				}
			}
			if exit, ok := m["exitCode"].(float64); ok {
				v.Result = "SUCCESS"
				if exit != 0 {
					v.Result = "TOOL_ERROR"
				}
			}
			if ms, ok := m["durationMs"].(float64); ok && ms >= 0 {
				v.Reported = &ms
			}
			// A completed structured item proves the attempt even when start time was not observed.
			start := v
			start.Kind = "call"
			start.Phase = "START_OBSERVED"
			start.Occurred = nil
			start.Action = ""
			start.ID = key(v.ID, "item")
			d.emit(start)
		}
		d.emit(v)
		if typ == "collabAgentToolCall" {
			for i, id := range stringsOf(m["receiverThreadIds"]) {
				child := collect.SessionKey(e.Profile, id)
				if (name == "spawnAgent" || name == "sendInput") && phase == "completed" && status == "completed" {
					a := d.event(r, dir, "assignment", i+1)
					a.Target = child
					a.Created = name == "spawnAgent"
					a.Action = conditional(a.Created, "SPAWN", "ASSIGN")
					d.emit(a)
				}
			}
			for _, id := range sortedKeys(obj(m["agentsStates"])) {
				st := text(obj(m["agentsStates"]), id)
				state := map[string]string{"running": "RUNNING", "completed": "ENDED", "errored": "ENDED", "shutdown": "ENDED"}[st]
				if state != "" {
					a := d.event(r, dir, "state", 0)
					a.ID = key(a.ID, id)
					a.Actor = collect.SessionKey(e.Profile, id)
					a.State = state
					a.Action = "STATE"
					d.emit(a)
				}
			}
		}
	case "userMessage", "agentMessage", "reasoning", "plan":
		v := d.event(r, dir, "message", 0)
		v.Action = "SEND"
		d.emit(v)
	}
}
func (d *decoder) otel(r store.Record) {
	e := r.Evidence
	m := e.Metadata
	name := text(m, "event.name")
	if name != "api_request_body" && name != "api_response_body" {
		d.m.unlinked[recID(r)] = true
		return
	}
	id := text(m, "request_body_id")
	if id == "" {
		d.m.unlinked[recID(r)] = true
		return
	}
	k := key(e.Source, e.Stream, id)
	a := d.m.models[k]
	if a == nil {
		a = &sourceModel{id: k, source: e.Source}
		d.m.models[k] = a
	}
	if text(m, "model") != "" {
		a.model = text(m, "model")
	}
	if text(m, "event.name") == "api_request_body" {
		a.request = uniq(a.request, recID(r))
		a.start = minTime(a.start, e.OccurredMS)
	} else if text(m, "event.name") == "api_response_body" {
		a.response = uniq(a.response, recID(r))
		a.end = maxTime(a.end, e.OccurredMS)
	}
}
func (d *decoder) claude(records []store.Record) {
	nodes := map[string]store.Record{}
	children := map[string]store.Record{}
	snapshot := map[string]direction{}
	nodeKey := func(r store.Record, id string) string {
		return key(streamID(r.Evidence), text(r.Evidence.Metadata, "sessionId"), id)
	}
	for _, r := range records {
		m := r.Evidence.Metadata
		nodes[nodeKey(r, text(m, "uuid"))] = r
		if child := text(m, "agentId"); child != "" {
			family := key(r.Evidence.Source, text(m, "sessionId"), child)
			if _, ok := children[family]; !ok {
				children[family] = r
			}
			session := collect.SessionKey(r.Evidence.Profile, text(m, "sessionId"))
			d.m.addIdentity(key(session, "agent", child), "", client(r.Evidence.Kind), []string{recID(r)})
		}
	}
	// Resolve parent chains once, including results delivered before their calls.
	resolve := func(r store.Record) direction {
		var path []string
		visited := map[string]bool{}
		var dir direction
		for {
			m := r.Evidence.Metadata
			k := nodeKey(r, text(m, "uuid"))
			if seen, ok := snapshot[k]; ok {
				dir = seen
				break
			}
			if visited[k] {
				break
			}
			visited[k] = true
			path = append(path, k)
			if collect.HumanPrompt(r.Evidence) {
				dir = direction{session: text(m, "sessionId"), turn: text(m, "promptId")}
				break
			}
			parent := text(m, "parentUuid")
			if parent == "" {
				break
			}
			var ok bool
			r, ok = nodes[nodeKey(r, parent)]
			if !ok {
				break
			}
		}
		for _, k := range path {
			snapshot[k] = dir
		}
		return dir
	}
	childDirection := claudeChildren(records, resolve)
	type deleg struct {
		event      FactEvent
		family, id string
	}
	var delegated []deleg
	agentCalls := map[string]FactEvent{}
	for _, r := range records {
		m := r.Evidence.Metadata
		dir := resolve(r)
		if text(m, "agentId") != "" {
			dir = childDirection(r)
		}
		msg := obj(m["message"])
		blocks := arr(msg["content"])
		if collect.HumanPrompt(r.Evidence) && dir.turn != "" {
			v := d.event(r, dir, "task", 0)
			v.Action = "SEND"
			v.State = "UNKNOWN"
			d.emit(v)
		}
		resultCount := 0
		for _, b := range blocks {
			if text(obj(b), "type") == "tool_result" {
				resultCount++
			}
		}
		for i, raw := range blocks {
			b := obj(raw)
			typ := text(b, "type")
			if typ != "tool_result" && typ != "tool_use" {
				continue
			}
			kind, id := "call", text(b, "id")
			if typ == "tool_result" {
				kind, id = "result", text(b, "tool_use_id")
			}
			if id == "" || dir.turn == "" {
				d.m.unlinked[recID(r)] = true
				continue
			}
			v := d.tool(r, dir, i, id, text(b, "name"), text(b, "mcpServerKey"), kind)
			if er, ok := b["is_error"].(bool); ok {
				v.Result = "SUCCESS"
				if er {
					v.Result = "TOOL_ERROR"
				}
			}
			if kind == "call" && v.Tool == "Agent" {
				agentCalls[v.Attempt] = v
			}
			d.emit(v)
			if child := text(obj(m["toolUseResult"]), "agentId"); child != "" && kind == "result" && resultCount == 1 {
				delegated = append(delegated, deleg{v, key(r.Evidence.Source, dir.session, child), child})
			}
		}
	}
	for _, g := range delegated {
		child, ok := children[g.family]
		call, called := agentCalls[g.event.Attempt]
		if !ok || !called {
			continue
		}
		v := g.event
		v.Kind = "assignment"
		v.Action = "ASSIGN"
		v.ID = key(v.ID, "assign")
		v.Target = key(v.Session, "agent", g.id)
		v.Evidence = append(slicesCopy(v.Evidence), recID(child))
		v.Created = true
		d.m.bodyRefs[v.ID] = d.m.bodyRefs[call.ID]
		v.Evidence = append(v.Evidence, call.Evidence...)
		d.emit(v)
	}
}
func slicesCopy[T any](s []T) []T { return append([]T(nil), s...) }
func jsonNumber(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// A child's own records may have a separate UUID chain. Only an unambiguous
// parent Agent call, result and explicit child identifier bridge the two files.
func claudeChildren(records []store.Record, root func(store.Record) direction) func(store.Record) direction {
	family := func(r store.Record, child string) string {
		return key(r.Evidence.Source, text(r.Evidence.Metadata, "sessionId"), child)
	}
	callKey := func(r store.Record, id string) string {
		return key(streamID(r.Evidence), text(r.Evidence.Metadata, "sessionId"), id)
	}
	agentCalls := map[string]store.Record{}
	callCount := map[string]int{}
	for _, r := range records {
		for _, b := range arr(obj(r.Evidence.Metadata["message"])["content"]) {
			block := obj(b)
			id := text(block, "id")
			if text(block, "type") == "tool_use" && id != "" {
				k := callKey(r, id)
				callCount[k]++
				if text(block, "name") == "Agent" {
					agentCalls[k] = r
				}
			}
		}
	}
	parents := map[string][]store.Record{}
	for _, r := range records {
		m := r.Evidence.Metadata
		child := text(obj(m["toolUseResult"]), "agentId")
		if child == "" {
			continue
		}
		var ids []string
		for _, b := range arr(obj(m["message"])["content"]) {
			if text(obj(b), "type") == "tool_result" {
				ids = append(ids, text(obj(b), "tool_use_id"))
			}
		}
		if len(ids) != 1 || ids[0] == "" {
			continue
		}
		k := callKey(r, ids[0])
		call, ok := agentCalls[k]
		if !ok || callCount[k] != 1 {
			continue
		}
		parents[family(r, child)] = append(parents[family(r, child)], call)
	}
	cache := map[string]direction{}
	active := map[string]bool{}
	var resolve func(store.Record) direction
	resolve = func(r store.Record) direction {
		m := r.Evidence.Metadata
		child := text(m, "agentId")
		if child == "" {
			return root(r)
		}
		k := family(r, child)
		if dir, ok := cache[k]; ok {
			return dir
		}
		if active[k] || len(active) > 512 {
			return direction{}
		}
		active[k] = true
		var resolved direction
		for _, parent := range parents[k] {
			dir := resolve(parent)
			if dir.turn == "" || resolved.turn != "" && (resolved.session != dir.session || resolved.turn != dir.turn) {
				resolved = direction{}
				break
			}
			resolved = direction{session: dir.session, turn: dir.turn, actor: key(collect.SessionKey(r.Evidence.Profile, dir.session), "agent", child)}
		}
		delete(active, k)
		cache[k] = resolved
		return resolved
	}
	return resolve
}
