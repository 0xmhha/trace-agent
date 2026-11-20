package collect

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
)

func (a *Adapter) rollout(r object) object {
	p := obj(r["payload"])
	q := object{}
	kind := str(r, "type")
	switch kind {
	case "session_meta":
		a.ids(q, p, "id", "parent_thread_id")
		nested := obj(obj(obj(p["source"])["subagent"])["thread_spawn"])
		if parent := str(nested, "parent_thread_id"); parent != "" {
			if top := str(p, "parent_thread_id"); top != "" && top != parent {
				delete(q, "parent_thread_id")
				q["parent_conflict"] = true
			} else {
				a.ids(q, nested, "parent_thread_id")
			}
		}
	case "event_msg":
		typ := choice(p, "type", "task_started", "task_complete")
		if typ == "" {
			return nil
		}
		q["type"] = typ
		a.ids(q, p, "turn_id")
	case "turn_context":
		a.ids(q, p, "turn_id", "model")
	case "response_item":
		typ := choice(p, "type", "function_call", "function_call_output", "custom_tool_call", "custom_tool_call_output", "message")
		if typ == "" {
			return nil
		}
		q["type"] = typ
		a.ids(q, p, "id", "call_id", "turn_id")
		switch typ {
		case "function_call", "custom_tool_call":
			q["name"] = a.tool(str(p, "name"))
		case "function_call_output", "custom_tool_call_output":
			// A downstream reader must still match the call ID and wait tool.
			q["output"] = a.waitReport(str(p, "output"))
		case "message":
			q["role"] = choice(p, "role", "user", "assistant", "system", "developer")
		}
	default:
		return nil
	}
	return object{"type": kind, "payload": q}
}

func (a *Adapter) transcript(r object) object {
	role := choice(r, "type", "user", "assistant")
	if role == "" {
		return nil
	}
	m := object{"type": role}
	a.ids(m, r, "sessionId", "uuid", "parentUuid", "agentId", "promptId", "requestId")
	copyBool(m, r, "isMeta")
	copyBool(m, r, "isSidechain")
	if str(r, "userType") == "external" {
		m["userType"] = "external"
	}
	src := obj(r["message"])
	msg := object{"role": choice(src, "role", "user", "assistant")}
	a.ids(msg, src, "id", "model")
	switch content := src["content"].(type) {
	case string:
		msg["content"] = ""
	case []any:
		blocks := make([]object, 0, len(content))
		for _, raw := range content {
			b := obj(raw)
			item := object{"type": "unknown"}
			switch typ := choice(b, "type", "tool_use", "tool_result", "text", "thinking"); typ {
			case "tool_use":
				item["type"] = typ
				a.ids(item, b, "id")
				item["name"] = a.tool(str(b, "name"))
			case "tool_result":
				item["type"] = typ
				a.ids(item, b, "tool_use_id")
				copyBool(item, b, "is_error")
			case "text", "thinking":
				item["type"] = typ
			}
			blocks = append(blocks, item)
		}
		msg["content"] = blocks
	}
	m["message"] = msg
	if result := obj(r["toolUseResult"]); str(result, "agentId") != "" {
		link := object{}
		a.ids(link, result, "agentId")
		m["toolUseResult"] = link
	}
	return m
}

func (a *Adapter) plan(r object) object {
	if str(r, "method") != "turn/plan/updated" {
		return nil
	}
	p := obj(r["params"])
	v := object{}
	a.ids(v, p, "threadId", "turnId")
	rows, _ := p["plan"].([]any)
	steps := make([]object, 0, len(rows))
	for _, row := range rows {
		steps = append(steps, object{"status": choice(obj(row), "status", "pending", "inProgress", "completed")})
	}
	v["plan"] = steps
	return object{"method": "turn/plan/updated", "params": v}
}

func (a *Adapter) waitReport(raw string) string {
	var report struct {
		TimedOut *bool                      `json:"timed_out"`
		Status   map[string]json.RawMessage `json:"status"`
	}
	if json.Unmarshal([]byte(raw), &report) != nil {
		return "{}"
	}
	safe := object{}
	if report.TimedOut != nil {
		safe["timed_out"] = *report.TimedOut
	}
	states := object{}
	for id, value := range report.Status {
		var state object
		if json.Unmarshal(value, &state) != nil {
			continue
		}
		for _, terminal := range []string{"completed", "errored"} {
			if _, ok := state[terminal]; ok {
				states[a.id(id)] = object{terminal: ""}
				break
			}
		}
	}
	if len(states) > 0 {
		safe["status"] = states
	}
	out, _ := json.Marshal(safe)
	return string(out)
}

// Preserve the grouping of unlisted MCP tools without exposing server or tool
// names. This annotation is determined by the name already covered by the
// semantic tag, so replay stays compatible with existing immutable records.
func (a *Adapter) annotateModules(e *Evidence, r object) {
	changed := false
	add := func(dst, src object) {
		name := str(src, "name")
		if dst == nil {
			return
		}
		parts := strings.SplitN(name, "__", 3)
		if len(parts) == 3 && parts[0] == "mcp" && parts[1] != "" && parts[2] != "" {
			dst["mcpServerKey"] = a.id(parts[1])
			changed = true
		}
	}
	switch e.Kind {
	case Rollout:
		p := obj(r["payload"])
		switch str(p, "type") {
		case "function_call", "custom_tool_call":
			add(obj(e.Metadata["payload"]), p)
		}
	case Transcript:
		raw, _ := obj(r["message"])["content"].([]any)
		safe, _ := obj(e.Metadata["message"])["content"].([]object)
		if len(raw) == len(safe) {
			for i, b := range safe {
				if str(b, "type") == "tool_use" {
					add(b, obj(raw[i]))
				}
			}
		}
	}
	if changed {
		encoded, _ := json.Marshal(e.Metadata)
		e.projectedHash = sha256.Sum256(encoded)
	}
}

func (a *Adapter) annotateLineage(e *Evidence, r object) {
	if e.Kind == Transcript && str(r, "type") == "attachment" {
		lineage := object{"type": "attachment"}
		a.ids(lineage, r, "sessionId", "uuid", "parentUuid")
		if len(lineage) != 4 {
			return
		}
		e.Metadata = lineage
		a.retag(e)
		return
	}

	if e.Kind != Rollout || str(r, "type") != "response_item" {
		return
	}
	p := obj(r["payload"])
	dst := obj(e.Metadata["payload"])
	if dst == nil {
		return
	}
	switch str(p, "type") {
	case "function_call":
		name := str(p, "name")
		ns := str(p, "namespace")
		spawnCall := (name == "spawn_agent" && (ns == "" || ns == "multi_agent_v1")) || name == "multi_agent_v1.spawn_agent"
		if !spawnCall {
			return
		}
		dst["coordination"] = "spawn"
	case "function_call_output":
		var v struct {
			AgentID string `json:"agent_id"`
		}
		if json.Unmarshal([]byte(str(p, "output")), &v) != nil || v.AgentID == "" {
			return
		}
		dst["reportedChildId"] = a.id(v.AgentID)
	default:
		return
	}
	a.retag(e)
}
func (a *Adapter) retag(e *Evidence) {
	encoded, _ := json.Marshal(e.Metadata)
	e.legacyTag = e.SemanticTag
	e.SemanticTag = a.token("metadata", a.source, string(encoded))
	e.projectedHash = sha256.Sum256(encoded)
}
