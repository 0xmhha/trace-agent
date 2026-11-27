package collect

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"

	"time"
)

// ClaudeHook and MCPObserver represent independently authorized observation sources.
const (
	ClaudeHook  Kind = "claude-hook"
	MCPObserver Kind = "mcp-observer"
)

// ObserverAdapter binds an observer to one authorized client source and optional
// MCP server. The observer shares the client's identity namespace, not its source ID.
func ObserverAdapter(parent *Adapter, kind Kind, server string) (*Adapter, error) {
	if parent == nil {
		return nil, errInput
	}
	// A server-bound observer needs a server name; any other observer must not have one.
	if spec, _ := lookup(kind); !AcceptsParent(kind, parent.kind) || spec.Server != (server != "") || len(server) > maxServerName {
		return nil, errInput
	}
	a := *parent
	a.kind = kind
	a.parentSource = parent.source
	a.observerServer = parent.id(server)
	a.source = parent.token("observer", string(kind), parent.source, a.observerServer)
	a.bodyEnabled = false
	return &a, nil
}

// ProjectObservation accepts only the source's event envelope and hashes all
// identifiers. Free text, tool inputs, results and paths never enter metadata.
func (a *Adapter) ProjectObservation(raw []byte) (Evidence, error) {
	if len(raw) > maxRecord {
		return Evidence{}, errLimit
	}
	var r object
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if !json.Valid(raw) || dec.Decode(&r) != nil {
		return Evidence{}, errInput
	}
	m := object{"clientSource": a.parentSource}
	var stamp, stream, id, action string
	switch a.kind {
	case ClaudeHook:
		action = choice(r, "hook_event_name", "PreToolUse", "PostToolUse", "PostToolUseFailure")
		if action == "" || str(r, "session_id") == "" || str(r, "tool_use_id") == "" {
			return Evidence{}, errInput
		}
		m["type"] = "hook"
		m["sessionId"] = a.id(str(r, "session_id"))
		m["clientCall"] = a.id(str(r, "tool_use_id"))
		id = a.token("hook", str(r, "session_id"), str(r, "tool_use_id"), action)
		stream = "hooks"
		stamp = str(r, "timestamp")
	case MCPObserver:
		action = choice(r, "action", "bridge", "receive", "return")
		rpcID := ""
		switch v := r["rpc_id"].(type) {
		case string:
			if v != "" {
				rpcID = "s:" + v
			}
		case json.Number:
			if i, err := v.Int64(); err == nil {
				rpcID = "n:" + strconv.FormatInt(i, 10)
			}
		}
		if r["version"] != json.Number("1") || action == "" || str(r, "connection_id") == "" || rpcID == "" || a.id(str(r, "server")) != a.observerServer || str(r, "clock_id") == "" {
			return Evidence{}, errInput
		}
		m["type"] = "mcp_observation"
		m["server"] = a.observerServer
		m["connection"] = a.id(str(r, "connection_id"))
		m["rpc"] = a.id(rpcID)
		m["clock"] = a.id(str(r, "clock_id"))
		stamp = str(r, "timestamp")
		if stamp == "" {
			return Evidence{}, errInput
		}
		stream = str(r, "connection_id")
		id = a.token("mcp", stream, rpcID, action)
		if action == "bridge" {
			if str(r, "session_id") == "" || str(r, "client_call_id") == "" {
				return Evidence{}, errInput
			}
			m["sessionId"] = a.id(str(r, "session_id"))
			m["clientCall"] = a.id(str(r, "client_call_id"))
			if t := str(r, "turn_id"); t != "" {
				m["turnId"] = a.id(t)
			}
			// Preserve conflicting bridge candidates so the reader can refuse both.
			id = a.token(id, str(r, "session_id"), str(r, "client_call_id"), str(r, "turn_id"))
		}
		if action == "return" {
			code := "UNKNOWN"
			if v, ok := r["is_error"].(bool); ok {
				code = "SUCCESS"
				if v {
					code = "TOOL_ERROR"
				}
			}
			if raw, exists := r["rpc_error_code"]; exists {
				n, ok := raw.(json.Number)
				if !ok {
					return Evidence{}, errInput
				}
				if _, err := n.Int64(); err != nil {
					return Evidence{}, errInput
				}
				code = "RPC_ERROR"
			}
			m["result"] = code
		}
	default:
		return Evidence{}, errors.New("adapter is not an observer")
	}
	var occurred *int64
	if stamp != "" {
		v, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return Evidence{}, errInput
		}
		ms := v.UnixMilli()
		occurred = &ms
	}
	m["action"] = action
	e := a.evidence(Position{Stream: stream, Generation: "observer-v1"}, m, occurred)
	e.RecordKey = id
	if spec, ok := lookup(a.kind); ok {
		e.Observer = spec.Reporter
	}
	return e, nil
}
