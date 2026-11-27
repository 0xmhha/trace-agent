package insight

import (
	"strings"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Built-in tool names the rules refer to. The usage package names only the
// tools its parsers treat specially; these are needed for advice alone.
const (
	toolNameEdit         = "Edit"
	toolNameWrite        = "Write"
	toolNameMultiEdit    = "MultiEdit"
	toolNameNotebookEdit = "NotebookEdit"
	toolNameRead         = "Read"
)

// settingPermissionsAllow is the settings key of the command allowlist.
const settingPermissionsAllow = "permissions.allow"

// Tool failure rate: a tool with enough calls fails too often.
//
// Decisions:
//   - The rate is errors out of all calls of the range. A call without a
//     result yet counts as not failed, so a running session cannot raise the
//     rate.
//   - MCP tools are left to the MCP server rule, which judges them per
//     server; reporting the same failures under both rules would double them.
const (
	toolErrorMinCalls int64 = 20
	toolErrorRatePct  int64 = 15
)

// toolErrorText is the message table of the tool failure rule.
type toolErrorText struct {
	title, detail text
	failed, rate  text
	// Advice for a failing Bash, for a failing file-editing tool and for
	// any other tool.
	bash, edit, general advice
}

func toolErrorMessages() toolErrorText {
	return toolErrorText{
		// title takes the tool and its failure rate in percent.
		title: text{
			usage.LocaleEnglish: "%[2]s%% of %[1]s calls failed",
			usage.LocaleKorean:  "%s 호출의 %s%%가 실패했습니다",
		},
		// detail takes the number of calls and the number of failures.
		detail: text{
			usage.LocaleEnglish: "%[2]s of %[1]s calls failed. The session timeline shows the error of each failed call.",
			usage.LocaleKorean:  "호출 %s회 중 %s회가 실패했습니다. 세션 타임라인에서 실패한 호출의 오류 내용을 확인할 수 있습니다.",
		},
		// failed takes the tool.
		failed: text{usage.LocaleEnglish: "Failed %s calls", usage.LocaleKorean: "실패한 %s 호출"},
		rate:   text{usage.LocaleEnglish: "Failure rate", usage.LocaleKorean: "실패율"},
		bash: advice{
			title: text{usage.LocaleEnglish: "Add frequent commands to the allowlist", usage.LocaleKorean: "자주 쓰는 명령을 허용 목록에 추가"},
			action: text{
				usage.LocaleEnglish: "If many commands failed because permission was denied or the sandbox blocked them, add read-only commands to the allowlist and check the sandbox settings.",
				usage.LocaleKorean:  "권한 거부나 샌드박스 차단으로 실패한 명령이 많다면 허용 목록에 읽기 전용 명령을 등록하고 샌드박스 설정을 확인합니다.",
			},
		},
		edit: advice{
			title: text{usage.LocaleEnglish: "Read files before editing them", usage.LocaleKorean: "고치기 전에 파일 먼저 읽기"},
			action: text{
				usage.LocaleEnglish: "Editing a file that was not read fails because its content does not match. Note in the project instructions that a file is read before it is edited.",
				usage.LocaleKorean:  "읽지 않은 파일을 고치면 내용이 맞지 않아 실패합니다. 고칠 파일을 먼저 읽도록 프로젝트 지침에 적어 둡니다.",
			},
		},
		general: advice{
			title: text{usage.LocaleEnglish: "Find the cause of the failed calls", usage.LocaleKorean: "실패한 호출의 원인 확인"},
			action: text{
				usage.LocaleEnglish: "If the same error keeps coming back, write down its cause and how to avoid it in the project instructions.",
				usage.LocaleKorean:  "같은 오류가 되풀이된다면 그 원인과 피하는 방법을 프로젝트 지침에 적어 둡니다.",
			},
		},
	}
}

func evalToolErrorRate(in usage.InsightInput) verdict {
	w, m := newWriter(in.Locale), toolErrorMessages()
	var v verdict
	for _, tool := range in.Tools.Tools {
		if isMCPTool(tool) || tool.Calls < toolErrorMinCalls {
			continue
		}
		v.judged = true
		if !atLeastPercent(tool.Errors, tool.Calls, toolErrorRatePct) {
			continue
		}
		name := w.name(tool.Name)
		v.findings = append(v.findings, finding{
			subject:  tool.Name,
			severity: rateSeverity(tool.Errors, tool.Calls, toolErrorRatePct),
			title:    w.say(m.title, name, formatShare(tool.Errors, tool.Calls)),
			detail:   w.say(m.detail, formatCount(tool.Calls), formatCount(tool.Errors)),
			evidence: []usage.Evidence{
				w.countOf(w.say(m.failed, name), tool.Errors, tool.Calls, unitCalls),
				w.share(w.say(m.rate), tool.Errors, tool.Calls),
			},
			recommendation: toolAdvice(w, m, tool.Name),
		})
	}
	return v
}

func isMCPTool(tool usage.ToolUsage) bool {
	return tool.Category == usage.ToolMCP || strings.HasPrefix(tool.Name, usage.MCPToolPrefix)
}

// toolAdvice is the next step for a failing tool. Tools without an entry of
// their own get the general advice.
func toolAdvice(w writer, m toolErrorText, name string) *usage.Recommendation {
	switch name {
	case usage.ToolNameBash:
		return w.recommend(usage.RecommendSetting, m.bash, settingPermissionsAllow)
	case toolNameEdit, toolNameWrite, toolNameMultiEdit, toolNameNotebookEdit:
		return w.recommend(usage.RecommendHabit, m.edit, toolNameRead)
	default:
		return w.recommend(usage.RecommendHabit, m.general, name)
	}
}

// MCP server failure rate: a server with enough calls fails too often. The
// rate is errors out of all calls, as in the tool rule.
const (
	mcpErrorMinCalls int64 = 10
	mcpErrorRatePct  int64 = 20
)

// mcpErrorText is the message table of the MCP server failure rule.
type mcpErrorText struct {
	title, detail text
	failed, rate  text
	advice        advice
}

func mcpErrorMessages() mcpErrorText {
	return mcpErrorText{
		// title takes the server and its failure rate in percent.
		title: text{
			usage.LocaleEnglish: "%[2]s%% of calls to MCP server %[1]s failed",
			usage.LocaleKorean:  "MCP 서버 %s 호출의 %s%%가 실패했습니다",
		},
		// detail takes the number of calls and the number of failures.
		detail: text{
			usage.LocaleEnglish: "%[2]s of %[1]s calls failed. The server may have disconnected, or its authentication may have expired.",
			usage.LocaleKorean:  "호출 %s회 중 %s회가 실패했습니다. 서버 연결이 끊겼거나 인증이 만료됐을 수 있습니다.",
		},
		// failed takes the server.
		failed: text{usage.LocaleEnglish: "Failed %s calls", usage.LocaleKorean: "실패한 %s 호출"},
		rate:   text{usage.LocaleEnglish: "Failure rate", usage.LocaleKorean: "실패율"},
		advice: advice{
			title: text{usage.LocaleEnglish: "Check the server connection and authentication", usage.LocaleKorean: "서버 연결과 인증 점검"},
			action: text{
				usage.LocaleEnglish: "Check that the MCP server is connected, and authenticate again if the authentication has expired.",
				usage.LocaleKorean:  "MCP 서버가 연결돼 있는지 확인하고, 인증이 만료됐다면 다시 인증합니다.",
			},
		},
	}
}

func evalMCPServerErrorRate(in usage.InsightInput) verdict {
	w, m := newWriter(in.Locale), mcpErrorMessages()
	var v verdict
	for _, server := range in.Tools.MCPServers {
		if server.Calls < mcpErrorMinCalls {
			continue
		}
		v.judged = true
		if !atLeastPercent(server.Errors, server.Calls, mcpErrorRatePct) {
			continue
		}
		name := w.name(server.Name)
		v.findings = append(v.findings, finding{
			subject:  server.Name,
			severity: rateSeverity(server.Errors, server.Calls, mcpErrorRatePct),
			title:    w.say(m.title, name, formatShare(server.Errors, server.Calls)),
			detail:   w.say(m.detail, formatCount(server.Calls), formatCount(server.Errors)),
			evidence: []usage.Evidence{
				w.countOf(w.say(m.failed, name), server.Errors, server.Calls, unitCalls),
				w.share(w.say(m.rate), server.Errors, server.Calls),
			},
			recommendation: w.recommend(usage.RecommendMCP, m.advice, server.Name),
		})
	}
	return v
}
