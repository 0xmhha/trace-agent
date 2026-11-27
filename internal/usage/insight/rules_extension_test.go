package insight

import (
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func TestUnusedExtensions(t *testing.T) {
	unused := func(subject, callLabel string, sessions float64, kind usage.RecommendationKind) wantInsight {
		return wantInsight{
			id:       "unused-extensions:" + subject,
			severity: usage.SeverityInfo,
			evidence: []usage.Evidence{ev(callLabel, 0, unitTimes), ev("구간의 세션", sessions, unitItems)},
			recKind:  kind,
		}
	}
	skillRow := usage.ToolUsage{Name: usage.ToolNameSkill, Category: usage.ToolSkill, Calls: 3}
	mcpRow := usage.ToolUsage{Name: "mcp__tracker__search", Category: usage.ToolMCP, Calls: 2}
	agentRow := usage.ToolUsage{Name: usage.ToolNameAgent, Category: usage.ToolAgent, Calls: 1}
	runRuleCases(t, ruleUnusedExtensions, []ruleCase{
		{
			name: "fires for every kind without calls",
			in: usage.InsightInput{
				Overview: usage.Overview{Sessions: 20},
				Tools:    usage.Tools{Tools: []usage.ToolUsage{builtin(usage.ToolNameBash, 300, 0)}},
			},
			want: outcomeFired,
			items: []wantInsight{
				unused(subjectSkill, "스킬 호출", 20, usage.RecommendSkill),
				unused(subjectMCP, "MCP 도구 호출", 20, usage.RecommendMCP),
				unused(subjectAgent, "하위 에이전트 호출", 20, usage.RecommendHabit),
			},
		},
		{
			name: "fires only for the kind without calls",
			in: usage.InsightInput{
				Overview: usage.Overview{Sessions: 45, Subagents: 2},
				Tools:    usage.Tools{Skills: []usage.GroupUsage{group("review", 4, 0)}},
			},
			want:  outcomeFired,
			items: []wantInsight{unused(subjectMCP, "MCP 도구 호출", 45, usage.RecommendMCP)},
		},
		{
			name: "counts a skill with attributed tokens as used",
			in: usage.InsightInput{
				Overview: usage.Overview{Sessions: 20},
				Tools: usage.Tools{
					Tools:  []usage.ToolUsage{mcpRow, agentRow},
					Skills: []usage.GroupUsage{{Name: "review", Tokens: &usage.TokenTotals{Output: 10}}},
				},
			},
			want: outcomePassed,
		},
		{
			name: "passes when every kind is used",
			in: usage.InsightInput{
				Overview: usage.Overview{Sessions: 20},
				Tools:    usage.Tools{Tools: []usage.ToolUsage{skillRow, mcpRow, agentRow}},
			},
			want: outcomePassed,
		},
		{
			name: "passes when the group rows show the calls",
			in: usage.InsightInput{
				Overview: usage.Overview{Sessions: 20},
				Tools: usage.Tools{
					Skills:     []usage.GroupUsage{group("review", 1, 0)},
					MCPServers: []usage.GroupUsage{group("tracker", 1, 0)},
					Subagents:  []usage.GroupUsage{group("explorer", 1, 0)},
				},
			},
			want: outcomePassed,
		},
		{name: "is skipped below the session count", in: usage.InsightInput{Overview: usage.Overview{Sessions: 19}}, want: outcomeSkipped},
	})
}

func TestExtErrorRate(t *testing.T) {
	sources := func(rows ...usage.ExtSourceRow) usage.InsightInput { return usage.InsightInput{Ext: rows} }
	runRuleCases(t, ruleExtErrorRate, []ruleCase{
		{
			name: "fires exactly on both thresholds",
			in:   sources(usage.ExtSourceRow{Source: "deploy-script", Events: 20, Errors: 4}),
			want: outcomeFired,
			items: []wantInsight{{
				id:        "ext-error-rate:deploy-script",
				severity:  usage.SeverityWarn,
				evidence:  []usage.Evidence{evOf("오류 이벤트", 4, 20, unitItems), ev("오류 비율", 20, unitPercent)},
				recKind:   usage.RecommendPlugin,
				recTarget: "deploy-script",
			}},
		},
		{
			name: "is critical at twice the threshold",
			in: sources(
				usage.ExtSourceRow{Source: "lint-runner", Events: 100, Errors: 40},
				usage.ExtSourceRow{Source: "deploy-script", Events: 100, Errors: 1},
			),
			want: outcomeFired,
			items: []wantInsight{{
				id:        "ext-error-rate:lint-runner",
				severity:  usage.SeverityCritical,
				evidence:  []usage.Evidence{evOf("오류 이벤트", 40, 100, unitItems), ev("오류 비율", 40, unitPercent)},
				recKind:   usage.RecommendPlugin,
				recTarget: "lint-runner",
			}},
		},
		{name: "passes below the rate", in: sources(usage.ExtSourceRow{Source: "deploy-script", Events: 100, Errors: 19}), want: outcomePassed},
		{name: "is skipped below the event count", in: sources(usage.ExtSourceRow{Source: "deploy-script", Events: 19, Errors: 19}), want: outcomeSkipped},
		{name: "is skipped without senders", in: usage.InsightInput{}, want: outcomeSkipped},
	})
}

func TestExtRejected(t *testing.T) {
	runRuleCases(t, ruleExtRejected, []ruleCase{
		{
			name: "fires and splits the count by sender",
			in: usage.InsightInput{
				ExtRejected: 7,
				Ext: []usage.ExtSourceRow{
					{Source: "src-b", Events: 5, Rejected: 1},
					{Source: "src-a", Events: 5, Rejected: 4},
					{Source: "src-c", Events: 5},
				},
			},
			want: outcomeFired,
			items: []wantInsight{{
				id:       "ext-rejected",
				severity: usage.SeverityWarn,
				evidence: []usage.Evidence{
					ev("거부된 입력", 7, unitCases),
					ev("src-a 출처의 거부", 4, unitCases),
					ev("src-b 출처의 거부", 1, unitCases),
					ev("출처를 알 수 없는 거부", 2, unitCases),
				},
				recKind: usage.RecommendPlugin,
			}},
		},
		{
			name: "fires on a single refusal without a sender",
			in:   usage.InsightInput{ExtRejected: 1},
			want: outcomeFired,
			items: []wantInsight{{
				id:       "ext-rejected",
				severity: usage.SeverityWarn,
				evidence: []usage.Evidence{ev("거부된 입력", 1, unitCases), ev("출처를 알 수 없는 거부", 1, unitCases)},
				recKind:  usage.RecommendPlugin,
			}},
		},
		{
			name: "names at most three senders",
			in: usage.InsightInput{
				ExtRejected: 10,
				Ext: []usage.ExtSourceRow{
					{Source: "src-a", Rejected: 1}, {Source: "src-b", Rejected: 2},
					{Source: "src-c", Rejected: 3}, {Source: "src-d", Rejected: 4},
				},
			},
			want: outcomeFired,
			items: []wantInsight{{
				id:       "ext-rejected",
				severity: usage.SeverityWarn,
				evidence: []usage.Evidence{
					ev("거부된 입력", 10, unitCases),
					ev("src-d 출처의 거부", 4, unitCases),
					ev("src-c 출처의 거부", 3, unitCases),
					ev("src-b 출처의 거부", 2, unitCases),
				},
				recKind: usage.RecommendPlugin,
			}},
		},
		{
			name: "passes when senders exist and nothing was refused",
			in:   usage.InsightInput{Ext: []usage.ExtSourceRow{{Source: "src-a", Events: 3}}},
			want: outcomePassed,
		},
		{name: "is skipped when the endpoint is unused", in: usage.InsightInput{}, want: outcomeSkipped},
	})
}
