package insight

import (
	"fmt"
	"strings"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// hookEventSeparator joins a hook event and a hook name in a subject.
const hookEventSeparator = "/"

// hookLabel names a hook in an insight ID and in its text. The transcript's
// hook name usually starts with its event already ("PreToolUse:Bash"), so the
// event is prefixed only when the name does not carry it. Without the event,
// two hooks that share a name under different events would share an ID.
func hookLabel(hook usage.HookUsage) string {
	switch {
	case hook.Name == "":
		return hook.Event
	case hook.Event == "" || strings.HasPrefix(hook.Name, hook.Event):
		return hook.Name
	default:
		return hook.Event + hookEventSeparator + hook.Name
	}
}

// Slow hook: a hook that runs often takes a second or more on average.
//
// Decisions:
//   - A hook whose average duration is unknown cannot be judged and does not
//     count as a sample.
//   - The severity doubles like a rate: critical from twice the threshold.
const (
	slowHookMinRuns int64   = 20
	slowHookAvgMs   float64 = 1000
)

func evalSlowHook(in usage.InsightInput) verdict {
	var v verdict
	for _, hook := range in.Tools.Hooks {
		if hook.Runs < slowHookMinRuns || hook.AvgMs == nil {
			continue
		}
		v.judged = true
		avg := *hook.AvgMs
		if avg < slowHookAvgMs {
			continue
		}
		severity := usage.SeverityWarn
		if avg >= slowHookAvgMs*criticalFactor {
			severity = usage.SeverityCritical
		}
		evidence := []usage.Evidence{
			millis("평균 실행 시간", avg),
			count("실행 횟수", hook.Runs, unitTimes),
		}
		if hook.MaxMs != nil {
			evidence = append(evidence, millis("가장 긴 실행 시간", *hook.MaxMs))
		}
		label := hookLabel(hook)
		v.findings = append(v.findings, finding{
			subject:  label,
			severity: severity,
			title:    fmt.Sprintf("%s 훅이 평균 %s 걸립니다", displayName(label), formatSeconds(avg)),
			detail: fmt.Sprintf("구간 안에서 %s회 실행됐습니다. 훅이 끝날 때까지 작업이 기다리므로 지연이 그대로 대기 시간이 됩니다.",
				formatCount(hook.Runs)),
			evidence: evidence,
			recommendation: &usage.Recommendation{
				Kind:   usage.RecommendHook,
				Title:  "훅 명령을 가볍게",
				Action: "무거운 검사는 바뀐 파일에만 돌리거나 백그라운드 작업으로 옮깁니다.",
			},
		})
	}
	return v
}

// Failing hook: a hook with enough runs fails too often. Failures are what
// the store counts as failed runs; the rate is failures out of all runs.
const (
	failingHookMinRuns int64 = 10
	failingHookRatePct int64 = 10
)

func evalFailingHook(in usage.InsightInput) verdict {
	var v verdict
	for _, hook := range in.Tools.Hooks {
		if hook.Runs < failingHookMinRuns {
			continue
		}
		v.judged = true
		if !atLeastPercent(hook.Failures, hook.Runs, failingHookRatePct) {
			continue
		}
		label := hookLabel(hook)
		v.findings = append(v.findings, finding{
			subject:  label,
			severity: rateSeverity(hook.Failures, hook.Runs, failingHookRatePct),
			title:    fmt.Sprintf("%s 훅 실행의 %s%%가 실패했습니다", displayName(label), formatShare(hook.Failures, hook.Runs)),
			detail: fmt.Sprintf("실행 %s회 중 %s회가 실패했습니다. 실패한 훅은 맡은 검사를 하지 못합니다.",
				formatCount(hook.Runs), formatCount(hook.Failures)),
			evidence: []usage.Evidence{
				countOf("실패한 훅 실행", hook.Failures, hook.Runs, unitTimes),
				share("실패율", hook.Failures, hook.Runs),
			},
			recommendation: &usage.Recommendation{
				Kind:   usage.RecommendHook,
				Title:  "훅 스크립트 점검",
				Action: "훅 명령을 터미널에서 직접 실행해 종료 코드와 오류 출력을 확인합니다.",
			},
		})
	}
	return v
}
