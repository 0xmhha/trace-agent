package insight

import (
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func toolsInput(tools ...usage.ToolUsage) usage.InsightInput {
	return usage.InsightInput{Tools: usage.Tools{Tools: tools}}
}

func TestToolErrorRate(t *testing.T) {
	mcpTool := usage.ToolUsage{Name: "mcp__tracker__search", Category: usage.ToolMCP, Calls: 100, Errors: 90}
	runRuleCases(t, ruleToolErrorRate, []ruleCase{
		{
			name: "fires with a warning for Bash",
			in:   toolsInput(builtin(usage.ToolNameBash, 900, 180)),
			want: outcomeFired,
			items: []wantInsight{{
				id:        "tool-error-rate:Bash",
				severity:  usage.SeverityWarn,
				evidence:  []usage.Evidence{evOf("실패한 Bash 호출", 180, 900, unitTimes), ev("실패율", 20, unitPercent)},
				recKind:   usage.RecommendSetting,
				recTarget: settingPermissionsAllow,
			}},
		},
		{
			name: "fires exactly on both thresholds",
			in:   toolsInput(builtin(toolNameEdit, 20, 3)),
			want: outcomeFired,
			items: []wantInsight{{
				id:        "tool-error-rate:Edit",
				severity:  usage.SeverityWarn,
				evidence:  []usage.Evidence{evOf("실패한 Edit 호출", 3, 20, unitTimes), ev("실패율", 15, unitPercent)},
				recKind:   usage.RecommendHabit,
				recTarget: toolNameRead,
			}},
		},
		{
			name: "is critical at twice the threshold",
			in:   toolsInput(builtin("Grep", 40, 12)),
			want: outcomeFired,
			items: []wantInsight{{
				id:        "tool-error-rate:Grep",
				severity:  usage.SeverityCritical,
				evidence:  []usage.Evidence{evOf("실패한 Grep 호출", 12, 40, unitTimes), ev("실패율", 30, unitPercent)},
				recKind:   usage.RecommendHabit,
				recTarget: "Grep",
			}},
		},
		{
			name: "fires once per failing tool and ignores healthy ones",
			in: toolsInput(
				builtin(toolNameRead, 500, 1),
				builtin(toolNameWrite, 30, 7),
				builtin(usage.ToolNameBash, 100, 29),
			),
			want: outcomeFired,
			items: []wantInsight{
				{
					id:        "tool-error-rate:Write",
					severity:  usage.SeverityWarn,
					evidence:  []usage.Evidence{evOf("실패한 Write 호출", 7, 30, unitTimes), ev("실패율", 23.3, unitPercent)},
					recKind:   usage.RecommendHabit,
					recTarget: toolNameRead,
				},
				{
					id:        "tool-error-rate:Bash",
					severity:  usage.SeverityWarn,
					evidence:  []usage.Evidence{evOf("실패한 Bash 호출", 29, 100, unitTimes), ev("실패율", 29, unitPercent)},
					recKind:   usage.RecommendSetting,
					recTarget: settingPermissionsAllow,
				},
			},
		},
		{name: "passes below the rate", in: toolsInput(builtin(usage.ToolNameBash, 100, 14)), want: outcomePassed},
		{name: "is skipped below the call count", in: toolsInput(builtin(usage.ToolNameBash, 19, 19)), want: outcomeSkipped},
		{name: "leaves MCP tools to the server rule", in: toolsInput(mcpTool), want: outcomeSkipped},
		{name: "is skipped without tools", in: usage.InsightInput{}, want: outcomeSkipped},
	})
}

func TestMCPServerErrorRate(t *testing.T) {
	servers := func(groups ...usage.GroupUsage) usage.InsightInput {
		return usage.InsightInput{Tools: usage.Tools{MCPServers: groups}}
	}
	runRuleCases(t, ruleMCPServerErrorRate, []ruleCase{
		{
			name: "fires exactly on both thresholds",
			in:   servers(group("tracker", 10, 2)),
			want: outcomeFired,
			items: []wantInsight{{
				id:        "mcp-server-error-rate:tracker",
				severity:  usage.SeverityWarn,
				evidence:  []usage.Evidence{evOf("실패한 tracker 호출", 2, 10, unitTimes), ev("실패율", 20, unitPercent)},
				recKind:   usage.RecommendMCP,
				recTarget: "tracker",
			}},
		},
		{
			name: "is critical at twice the threshold",
			in:   servers(group("docs", 50, 20), group("tracker", 200, 1)),
			want: outcomeFired,
			items: []wantInsight{{
				id:        "mcp-server-error-rate:docs",
				severity:  usage.SeverityCritical,
				evidence:  []usage.Evidence{evOf("실패한 docs 호출", 20, 50, unitTimes), ev("실패율", 40, unitPercent)},
				recKind:   usage.RecommendMCP,
				recTarget: "docs",
			}},
		},
		{name: "passes below the rate", in: servers(group("tracker", 100, 19)), want: outcomePassed},
		{name: "is skipped below the call count", in: servers(group("tracker", 9, 9)), want: outcomeSkipped},
		{name: "is skipped without servers", in: usage.InsightInput{}, want: outcomeSkipped},
	})
}
