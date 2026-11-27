package insight

import (
	"fmt"
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

func evalToolErrorRate(in usage.InsightInput) verdict {
	var v verdict
	for _, tool := range in.Tools.Tools {
		if isMCPTool(tool) || tool.Calls < toolErrorMinCalls {
			continue
		}
		v.judged = true
		if !atLeastPercent(tool.Errors, tool.Calls, toolErrorRatePct) {
			continue
		}
		name := displayName(tool.Name)
		v.findings = append(v.findings, finding{
			subject:  tool.Name,
			severity: rateSeverity(tool.Errors, tool.Calls, toolErrorRatePct),
			title:    fmt.Sprintf("%s 호출의 %s%%가 실패했습니다", name, formatShare(tool.Errors, tool.Calls)),
			detail: fmt.Sprintf("호출 %s회 중 %s회가 실패했습니다. 세션 타임라인에서 실패한 호출의 오류 내용을 확인할 수 있습니다.",
				formatCount(tool.Calls), formatCount(tool.Errors)),
			evidence: []usage.Evidence{
				countOf("실패한 "+name+" 호출", tool.Errors, tool.Calls, unitTimes),
				share("실패율", tool.Errors, tool.Calls),
			},
			recommendation: toolAdvice(tool.Name),
		})
	}
	return v
}

func isMCPTool(tool usage.ToolUsage) bool {
	return tool.Category == usage.ToolMCP || strings.HasPrefix(tool.Name, usage.MCPToolPrefix)
}

// toolAdvice is the next step for a failing tool. Tools without an entry of
// their own get the general advice.
func toolAdvice(name string) *usage.Recommendation {
	switch name {
	case usage.ToolNameBash:
		return &usage.Recommendation{
			Kind:   usage.RecommendSetting,
			Title:  "자주 쓰는 명령을 허용 목록에 추가",
			Action: "권한 거부나 샌드박스 차단으로 실패한 명령이 많다면 허용 목록에 읽기 전용 명령을 등록하고 샌드박스 설정을 확인합니다.",
			Target: settingPermissionsAllow,
		}
	case toolNameEdit, toolNameWrite, toolNameMultiEdit, toolNameNotebookEdit:
		return &usage.Recommendation{
			Kind:   usage.RecommendHabit,
			Title:  "고치기 전에 파일 먼저 읽기",
			Action: "읽지 않은 파일을 고치면 내용이 맞지 않아 실패합니다. 고칠 파일을 먼저 읽도록 프로젝트 지침에 적어 둡니다.",
			Target: toolNameRead,
		}
	default:
		return &usage.Recommendation{
			Kind:   usage.RecommendHabit,
			Title:  "실패한 호출의 원인 확인",
			Action: "같은 오류가 되풀이된다면 그 원인과 피하는 방법을 프로젝트 지침에 적어 둡니다.",
			Target: name,
		}
	}
}

// MCP server failure rate: a server with enough calls fails too often. The
// rate is errors out of all calls, as in the tool rule.
const (
	mcpErrorMinCalls int64 = 10
	mcpErrorRatePct  int64 = 20
)

func evalMCPServerErrorRate(in usage.InsightInput) verdict {
	var v verdict
	for _, server := range in.Tools.MCPServers {
		if server.Calls < mcpErrorMinCalls {
			continue
		}
		v.judged = true
		if !atLeastPercent(server.Errors, server.Calls, mcpErrorRatePct) {
			continue
		}
		name := displayName(server.Name)
		v.findings = append(v.findings, finding{
			subject:  server.Name,
			severity: rateSeverity(server.Errors, server.Calls, mcpErrorRatePct),
			title:    fmt.Sprintf("MCP 서버 %s 호출의 %s%%가 실패했습니다", name, formatShare(server.Errors, server.Calls)),
			detail: fmt.Sprintf("호출 %s회 중 %s회가 실패했습니다. 서버 연결이 끊겼거나 인증이 만료됐을 수 있습니다.",
				formatCount(server.Calls), formatCount(server.Errors)),
			evidence: []usage.Evidence{
				countOf("실패한 "+name+" 호출", server.Errors, server.Calls, unitTimes),
				share("실패율", server.Errors, server.Calls),
			},
			recommendation: &usage.Recommendation{
				Kind:   usage.RecommendMCP,
				Title:  "서버 연결과 인증 점검",
				Action: "MCP 서버가 연결돼 있는지 확인하고, 인증이 만료됐다면 다시 인증합니다.",
				Target: server.Name,
			},
		})
	}
	return v
}
