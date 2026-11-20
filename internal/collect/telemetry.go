package collect

// Telemetry keeps the producer's correlation namespaces distinct. An event
// without a supported cross-source bridge remains source-scoped evidence.
func (a *Adapter) telemetry(r object) object {
	name := choice(r, "event.name", "api_request_body", "api_response_body", "api_request", "api_error", "user_prompt", "assistant_response", "tool_result", "tool_decision")
	if name == "" {
		return nil
	}
	m := object{"event.name": name}
	a.ids(m, r, "request_body_id", "message.uuid", "request_id", "message.id", "model")
	if name != "api_request_body" && name != "api_response_body" {
		a.ids(m, r, "prompt.id", "session.id", "tool_use_id")
	}
	return m
}
