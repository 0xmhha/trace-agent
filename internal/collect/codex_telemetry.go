package collect

import (
	"math"
	"strconv"
)

// A Codex retry ordinal is local to its HTTP request. It cannot identify a
// model turn or pair a later SSE, decision or tool observation with the request.
func (a *Adapter) codexTelemetry(r object) object {
	name := choice(r, "event.name", "codex.conversation_starts", "codex.startup_phase", "codex.user_prompt", "codex.api_request", "codex.sse_event", "codex.turn_ttft", "codex.tool_decision", "codex.tool_result")
	if name == "" {
		return nil
	}
	m := object{"event.name": name}
	a.ids(m, r, "conversation.id", "model")
	copyInt := func(k string, lower, upper int64) {
		v, err := strconv.ParseInt(str(r, k), 10, 64)
		if err == nil && v >= lower && v <= upper {
			m[k] = v
		}
	}
	const maxExactJSONInteger = 1<<53 - 1
	switch name {
	case "codex.api_request":
		copyInt("attempt", 0, math.MaxInt32)
		copyInt("http.response.status_code", 100, 599)
	case "codex.tool_decision", "codex.tool_result":
		a.ids(m, r, "call_id")
		if tool := a.tool(str(r, "tool_name")); tool != "" {
			m["tool_name"] = tool
		}
		if name == "codex.tool_result" {
			if success := choice(r, "success", "true", "false"); success != "" {
				m["success"] = success == "true"
			}
		}
	}
	switch name {
	case "codex.startup_phase", "codex.api_request", "codex.sse_event", "codex.turn_ttft", "codex.tool_result":
		copyInt("duration_ms", 0, maxExactJSONInteger)
	}
	return m
}
