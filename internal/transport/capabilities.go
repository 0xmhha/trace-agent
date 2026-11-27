package transport

import (
	"github.com/0xmhha/trace-agent/internal/collect"
	"github.com/0xmhha/trace-agent/internal/store"
)

func capability(code, state, reason string) object {
	return object{"code": code, "state": state, "reason": reason}
}
func capabilities(i store.SourceInfo) []any {
	items := []any{capability("desktop_live", "UNVERIFIED", "Notifications and restarts of work started in a separate desktop window have not been verified.")}
	// A stored kind this build does not register has neither property.
	info, _ := collect.KindInfo(collect.Kind(i.Kind))
	observer, telemetry := info.Observer, info.OTLP
	codexOTel := i.Kind == string(collect.CodexTelemetry)
	if telemetry {
		reason := "OTLP/HTTP protobuf and JSON export from Claude Code CLI 2.1.329 was verified."
		state := "AVAILABLE"
		if codexOTel {
			reason = "API attempt numbers, HTTP statuses and durations sent by Codex CLI 0.169.1 over OTLP/HTTP protobuf and JSON were verified."
			if i.ClientVersion != "0.169.1" {
				state = "UNVERIFIED"
				reason = "Export was verified with Codex CLI 0.169.1. The version of this source is outside the verified range."
			}
		}
		items = append(items, capability("history", "UNAVAILABLE", "Events the SDK sends are collected once the OTLP receiver is connected. Earlier records cannot be requested."),
			capability("telemetry", state, reason),
			capability("restart", "PARTIAL", "The same receiving address and source credential are restored. Events the sender retries are treated as the same record."))
	} else if observer {
		items = append(items, capability("history", "UNAVAILABLE", "Only events delivered to the selected observer receiver are recorded. Earlier history cannot be queried at the origin."))
	} else {
		history := capability("history", "UNVERIFIED", "Existing evidence delivered by this source has not been checked yet.")
		if i.HasMetadata {
			history = capability("history", "AVAILABLE", "Evidence has been collected up to that point in the selected range.")
		}
		items = append(items, history)
	}
	switch collect.Kind(i.Kind) {
	case collect.ClaudeHook:
		items = append(items, capability("hook", "PARTIAL", "Pre-run and end events of registered hooks are received. Pre-run events are not used as evidence of completion."))
	case collect.MCPObserver:
		items = append(items, capability("mcp_observer", "PARTIAL", "Only requests and responses reported by explicitly connected MCP servers are checked. Matching them to a client needs shared connection evidence."))

	case collect.AppServer:
		items = append(items, capability("cli_history", "AVAILABLE", "Thread, turn and item history of Codex CLI 0.169.1 was read."),
			capability("live", "PARTIAL", "History of the selected working directory is read again periodically. Immediate delivery of notifications from other client connections is unverified."),
			capability("restart", "AVAILABLE", "The app-server of the same profile was restarted and the same history items were matched."),
			capability("plan", "PARTIAL", "Step projection of plan change notifications was verified on synthetic samples. Real CLI plan updates are unverified."))
	case collect.Rollout:
		items = append(items, capability("cli", "AVAILABLE", "Regular commands and child spawning were verified with Codex CLI 0.169.1."),
			capability("live", "AVAILABLE", "Complete lines were collected while a real CLI turn was in progress."),
			capability("restart", "AVAILABLE", "Reading resumes from the stored file position, and the last incomplete line is held until the next read."))
	case collect.Transcript:
		items = append(items, capability("cli", "AVAILABLE", "Tool round trips and child files were verified with Claude Code CLI 2.1.329."),
			capability("live", "AVAILABLE", "Complete lines are collected from the selected session file. Reading its child files as well requires the project folder."),
			capability("restart", "AVAILABLE", "The same file resumes from the SQLite checkpoint, and a replaced or truncated file is recorded as a gap."))
	}
	verifiedVersion := "2.1.329"
	if i.Client == "codex" {
		verifiedVersion = "0.169.1"
	}
	if i.ClientVersion == "" {
		items = append(items, capability("client_version", "UNVERIFIED", "The evidence of this source has no client version."))
	}
	if i.ClientVersion != "" && i.ClientVersion != verifiedVersion {
		items = append(items, capability("client_version", "UNVERIFIED", "The observed client version differs from the version the installed-build compatibility test used."))
	}

	if i.Client == "codex" && !codexOTel {
		items = append(items, capability("telemetry", "UNAVAILABLE", "Codex OTel events are collected once a dedicated source is connected."))
	}
	if observer {
		items = append(items, capability("body", "UNAVAILABLE", "This observation path collects only identifiers and status."))
	} else if i.Consent {
		reason := "Only supported fields for which both the event time and the current consent are valid are sanitized and shown."
		if telemetry {
			reason = "Only supported fields sent after the sender turned on the body option for this loopback address are sanitized."
		}
		if codexOTel {
			reason = "User input is sanitized only when the CLI's log_user_prompt option is on and body content is allowed for the source."
		}
		items = append(items, capability("body", "PARTIAL", reason))
	} else {
		items = append(items, capability("body", "UNAVAILABLE", "Body consent for this source is off."))
	}
	if !i.Enabled {
		items = append(items, capability("collection", "UNAVAILABLE", "Collection was stopped in the source settings."))
	}
	return items
}

func gapReason(code string) string {
	switch code {
	case "replaced":
		return "The file was replaced, so reading started from the beginning of the new file."
	case "truncated":
		return "The file was truncated, so reading could not continue from the stored position."
	case "rewritten":
		return "Existing content of the file changed, so it was read as a new generation."
	case "lost":
		return "The selected source could not be reached."
	case "delayed":
		return "Collection was delayed."
	case "erased_range":
		return "Evidence for this period was removed by deletion or retention expiry, and collecting it again is blocked."
	default:
		return "The source reported a collection gap."
	}
}
