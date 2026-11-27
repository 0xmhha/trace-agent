package insight

import (
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func hooksInput(hooks ...usage.HookUsage) usage.InsightInput {
	return usage.InsightInput{Tools: usage.Tools{Hooks: hooks}}
}

func TestSlowHook(t *testing.T) {
	runRuleCases(t, ruleSlowHook, []ruleCase{
		{
			name: "fires with a warning",
			in:   hooksInput(usage.HookUsage{Event: "Stop", Name: "Stop", Runs: 198, AvgMs: ptr(1240), MaxMs: ptr(3000)}),
			want: outcomeFired,
			items: []wantInsight{{
				id:       "slow-hook:Stop",
				severity: usage.SeverityWarn,
				evidence: []usage.Evidence{
					ev("평균 실행 시간", 1240, unitMillis),
					ev("실행 횟수", 198, unitTimes),
					ev("가장 긴 실행 시간", 3000, unitMillis),
				},
				recKind: usage.RecommendHook,
			}},
		},
		{
			name: "is critical at twice the threshold",
			in:   hooksInput(usage.HookUsage{Event: "PreToolUse", Name: "PreToolUse:Bash", Runs: 20, AvgMs: ptr(2000)}),
			want: outcomeFired,
			items: []wantInsight{{
				id:       "slow-hook:PreToolUse:Bash",
				severity: usage.SeverityCritical,
				evidence: []usage.Evidence{ev("평균 실행 시간", 2000, unitMillis), ev("실행 횟수", 20, unitTimes)},
				recKind:  usage.RecommendHook,
			}},
		},
		{
			name: "prefixes the event when the name lacks it",
			in:   hooksInput(usage.HookUsage{Event: "PostToolUse", Name: "format", Runs: 30, AvgMs: ptr(1000)}),
			want: outcomeFired,
			items: []wantInsight{{
				id:       "slow-hook:PostToolUse/format",
				severity: usage.SeverityWarn,
				evidence: []usage.Evidence{ev("평균 실행 시간", 1000, unitMillis), ev("실행 횟수", 30, unitTimes)},
				recKind:  usage.RecommendHook,
			}},
		},
		{
			name: "passes below the average",
			in:   hooksInput(usage.HookUsage{Event: "Stop", Name: "Stop", Runs: 20, AvgMs: ptr(999.9)}),
			want: outcomePassed,
		},
		{
			name: "is skipped below the run count",
			in:   hooksInput(usage.HookUsage{Event: "Stop", Name: "Stop", Runs: 19, AvgMs: ptr(5000)}),
			want: outcomeSkipped,
		},
		{
			name: "is skipped when the duration is unknown",
			in:   hooksInput(usage.HookUsage{Event: "Stop", Name: "Stop", Runs: 50}),
			want: outcomeSkipped,
		},
		{name: "is skipped without hooks", in: usage.InsightInput{}, want: outcomeSkipped},
	})
}

func TestFailingHook(t *testing.T) {
	runRuleCases(t, ruleFailingHook, []ruleCase{
		{
			name: "fires exactly on both thresholds",
			in:   hooksInput(usage.HookUsage{Event: "Stop", Name: "Stop", Runs: 10, Failures: 1}),
			want: outcomeFired,
			items: []wantInsight{{
				id:       "failing-hook:Stop",
				severity: usage.SeverityWarn,
				evidence: []usage.Evidence{evOf("실패한 훅 실행", 1, 10, unitTimes), ev("실패율", 10, unitPercent)},
				recKind:  usage.RecommendHook,
			}},
		},
		{
			name: "is critical at twice the threshold",
			in: hooksInput(
				usage.HookUsage{Event: "SessionStart", Name: "", Runs: 50, Failures: 10},
				usage.HookUsage{Event: "Stop", Name: "Stop", Runs: 50, Failures: 4},
			),
			want: outcomeFired,
			items: []wantInsight{{
				id:       "failing-hook:SessionStart",
				severity: usage.SeverityCritical,
				evidence: []usage.Evidence{evOf("실패한 훅 실행", 10, 50, unitTimes), ev("실패율", 20, unitPercent)},
				recKind:  usage.RecommendHook,
			}},
		},
		{
			name: "passes below the rate",
			in:   hooksInput(usage.HookUsage{Event: "Stop", Name: "Stop", Runs: 100, Failures: 9}),
			want: outcomePassed,
		},
		{
			name: "is skipped below the run count",
			in:   hooksInput(usage.HookUsage{Event: "Stop", Name: "Stop", Runs: 9, Failures: 9}),
			want: outcomeSkipped,
		},
		{name: "is skipped without hooks", in: usage.InsightInput{}, want: outcomeSkipped},
	})
}

func TestHookLabel(t *testing.T) {
	for _, tc := range []struct {
		name string
		hook usage.HookUsage
		want string
	}{
		{"name equal to event", usage.HookUsage{Event: "Stop", Name: "Stop"}, "Stop"},
		{"name carries event", usage.HookUsage{Event: "PreToolUse", Name: "PreToolUse:Bash"}, "PreToolUse:Bash"},
		{"name without event", usage.HookUsage{Event: "PostToolUse", Name: "format"}, "PostToolUse/format"},
		{"empty name", usage.HookUsage{Event: "Stop"}, "Stop"},
		{"empty event", usage.HookUsage{Name: "format"}, "format"},
		{"both empty", usage.HookUsage{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := hookLabel(tc.hook); got != tc.want {
				t.Fatalf("hookLabel = %q, want %q", got, tc.want)
			}
		})
	}
}
