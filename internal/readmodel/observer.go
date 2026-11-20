package readmodel

import (
	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

type deliveryEdge struct{ actor, action, direction, observer string }
type clientCall struct {
	event  FactEvent
	server string
}

func (d *decoder) observedEdges(records []store.Record) {
	var observations []store.Record
	for _, r := range records {
		if r.Evidence.Kind == collect.ClaudeHook || r.Evidence.Kind == collect.MCPObserver {
			at := r.Evidence.OccurredMS
			if at == nil || d.m.opts.Until.IsZero() || *at < d.m.opts.Until.UnixMilli() {
				observations = append(observations, r)
			}
		}
	}
	if len(observations) == 0 {
		return
	}
	// Candidate IDs are usable only inside the named original source, profile
	// and session. The observer cannot authorize another source by naming it.
	calls := map[string]map[string]clientCall{}
	for _, e := range d.events {
		if e.Kind != "call" || len(e.Evidence) != 1 {
			continue
		}
		r := d.m.records[e.Evidence[0]]
		meta := r.Evidence.Metadata
		var id, server string
		switch r.Evidence.Kind {
		case collect.Rollout:
			p := obj(meta["payload"])
			id, server = text(p, "call_id"), text(p, "mcpServerKey")
		case collect.AppServer:
			id, server = text(meta, "id"), text(meta, "server")
		case collect.Transcript:
			blocks := arr(obj(meta["message"])["content"])
			index := d.m.bodyRefs[e.ID].ordinal
			if index >= len(blocks) {
				continue
			}
			b := obj(blocks[index])
			id, server = text(b, "id"), text(b, "mcpServerKey")
		}
		if id == "" {
			continue
		}
		k := key(r.Evidence.Source, r.Evidence.Profile, e.Session, id)
		if calls[k] == nil {
			calls[k] = map[string]clientCall{}
		}
		calls[k][e.Attempt] = clientCall{e, server}
	}
	resolve := func(r store.Record, server string) (FactEvent, bool, bool) {
		meta := r.Evidence.Metadata
		profile := r.Evidence.Profile
		session := text(meta, "sessionId")
		k := key(text(meta, "clientSource"), profile, collect.SessionKey(profile, session), text(meta, "clientCall"))
		var out FactEvent
		for _, c := range calls[k] {
			if server != "" && (c.event.ModuleKind != "MCP_TOOL" || c.server != server) {
				continue
			}
			if turn := text(meta, "turnId"); turn != "" && collect.TaskKey(profile, session, turn) != c.event.Task {
				continue
			}
			if out.ID != "" {
				return FactEvent{}, false, true
			}
			out = c.event
		}
		return out, out.ID != "", false
	}
	rpc := func(r store.Record) string {
		m := r.Evidence.Metadata
		return key(r.Evidence.Source, text(m, "server"), text(m, "connection"), text(m, "rpc"))
	}
	bridges := map[string][]store.Record{}
	for _, r := range observations {
		if text(r.Evidence.Metadata, "action") == "bridge" {
			k := rpc(r)
			bridges[k] = append(bridges[k], r)
		}
	}
	for _, r := range observations {
		e := r.Evidence
		meta := e.Metadata
		action := text(meta, "action")
		bridge := r
		var server string
		if e.Kind == collect.MCPObserver {
			server = text(meta, "server")
			if len(bridges[rpc(r)]) != 1 {
				d.m.unlinked[recID(r)] = true
				d.m.conflict[recID(r)] = len(bridges[rpc(r)]) > 1
				continue
			}
			bridge = bridges[rpc(r)][0]
		}
		call, ok, ambiguous := resolve(bridge, server)
		if !ok {
			d.m.unlinked[recID(r)] = true
			d.m.conflict[recID(r)] = ambiguous
			continue
		}
		// Keep the native attempt ID. A hook or server cannot create a new call.
		v := call
		v.ID = key("observer", e.Source, e.RecordKey)
		v.Kind = "observer_edge"
		v.Source = e.Source
		v.Action = "STATE"
		v.Grade = "DIRECT"
		v.Phase = ""
		v.Result = ""
		v.Parents = nil
		v.ValidUntil = nil
		v.Ordered = false
		v.Occurred = e.OccurredMS
		v.Observed = e.ObservedMS
		v.Clock = key(e.Source, text(meta, "clock"))
		v.Stream = e.Stream
		v.Position = e.Position
		v.Evidence = uniq(slicesCopy(call.Evidence), recID(r))
		v.Evidence = uniq(v.Evidence, recID(bridge))
		edge := deliveryEdge{}
		switch action {
		case "bridge":
			v.Action = ""
			v.Kind = "observer_bridge"
		case "PreToolUse":
			edge = deliveryEdge{"CLIENT", "PROPOSE", "TOOL_PROPOSAL", "CLIENT"}
			v.Side = "LOCAL"
		case "PostToolUse", "PostToolUseFailure":
			edge = deliveryEdge{"AGENT", "RECEIVE", "TOOL_RESULT", "CLIENT"}
			v.Action = "RETURN"
			v.Side = "RECEIVER"
			v.Kind = "observer_result"
			v.Result = conditional(action == "PostToolUse", "SUCCESS", "TOOL_ERROR")
		case "receive":
			edge = deliveryEdge{"TOOL", "RECEIVE", "TOOL_REQUEST", "TOOL"}
			v.Action = "CALL"
			v.Side = "RECEIVER"
		case "return":
			edge = deliveryEdge{"TOOL", "RETURN", "TOOL_RESULT", "TOOL"}
			v.Action = "RETURN"
			v.Side = "SENDER"
			v.Kind = "observer_result"
			v.Result = text(meta, "result")
		default:
			continue
		}
		d.m.edges[v.ID] = edge
		d.emit(v)
	}
}
