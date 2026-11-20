package collect

import (
	"encoding/json"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// BodySlot holds only a sanitized, optional display field. It never enters
// evidence metadata, semantic tags, or the general-purpose read model.
type BodySlot struct{ Field, Status, Text, Role string }

// WithBodyCollection opts this adapter instance into in-memory field redaction.
// The writer must independently verify both historical and current consent.
func (a *Adapter) WithBodyCollection() *Adapter { b := *a; b.bodyEnabled = true; return &b }

// BodySlots returns copies of fields prepared while the adapter was opted in.
func (e Evidence) BodySlots() []BodySlot { return slices.Clone(e.slots) }

var sensitive = regexp.MustCompile(`(?i)(authorization|bearer|password|passwd|cookie|credential|private[ _-]?key|access[ _-]?key|api[ _-]?key|secret|token[ :=_-]|AKIA[A-Z0-9]{16}|sk-[[:alnum:]_-]{8}|gh[pousr]_[[:alnum:]]|https?://|file://|/users/|/home/|-----BEGIN|[[:alnum:]_.+-]+@[[:alnum:].-]+|[[:alnum:]_+/=-]{28,}|[0-9]{1,3}(\.[0-9]{1,3}){3}|[A-Za-z0-9_]+\s*=|비밀번호|인증키|접속키)`)

func cleanText(field, role, text string) BodySlot {
	b := BodySlot{Field: field, Role: role, Status: "redaction_failed"}
	if !utf8.ValidString(text) || len(text) > 64<<10 || strings.ContainsAny(text, "{}[]") || sensitive.MatchString(text) {
		return b
	}
	if strings.IndexFunc(text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' && r != '\r' }) >= 0 {
		return b
	}
	b.Status = "sanitized"
	rs := []rune(text)
	if len(rs) > 1024 {
		rs = rs[:1024]
		b.Status = "truncated"
	}
	b.Text = string(rs)
	return b
}

func bodyFields(kind Kind, r object) []BodySlot {
	var out []BodySlot
	add := func(field, role string, v any) {
		if s, ok := v.(string); ok && s != "" {
			out = append(out, cleanText(field, role, s))
		}
	}
	blocks := func(content any, role string) {
		if _, ok := content.(string); ok {
			add("message", role, content)
			return
		}
		list, _ := content.([]any)
		for i, v := range list {
			b := obj(v)
			suffix := "." + strconv.Itoa(i)
			switch str(b, "type") {
			case "text", "input_text", "output_text":
				add("text"+suffix, role, b["text"])
			case "tool_result":
				add("result"+suffix, "tool", b["content"])
			case "tool_use":
				input := obj(b["input"])
				add("mission"+suffix, role, input["prompt"])
			}
		}
	}
	switch kind {
	case Transcript:
		msg := obj(r["message"])
		if role := choice(msg, "role", "assistant", "user"); role != "" {
			blocks(msg["content"], role)
		}
	case Rollout:
		p := obj(r["payload"])
		switch str(p, "type") {
		case "message":
			blocks(p["content"], choice(p, "role", "user", "assistant", "system", "developer"))
		case "function_call_output", "custom_tool_call_output":
			add("result", "tool", p["output"])
		case "function_call":
			if str(p, "name") == "spawn_agent" {
				var args object
				if json.Unmarshal([]byte(str(p, "arguments")), &args) == nil {
					add("mission", "assistant", args["message"])
				}
			}
		}
	case AppServer:
		p := obj(r["params"])
		if str(r, "method") == "turn/plan/updated" {
			steps, _ := p["plan"].([]any)
			for i, v := range steps {
				add("step."+strconv.Itoa(i), "assistant", obj(v)["step"])
			}
		}
	case Telemetry:
		var payload object
		if json.Unmarshal([]byte(str(r, "body")), &payload) == nil {
			if str(r, "event.name") == "api_response_body" {
				blocks(payload["content"], "assistant")
			} else {
				msgs, _ := payload["messages"].([]any)
				if len(msgs) > 0 {
					msg := obj(msgs[len(msgs)-1])
					blocks(msg["content"], choice(msg, "role", "assistant", "user"))
				}
			}
		}
	}
	return out
}
