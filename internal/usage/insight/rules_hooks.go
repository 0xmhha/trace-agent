package insight

import (
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

// slowHookText is the message table of the slow hook rule.
type slowHookText struct {
	title, detail       text
	average, runs, peak text
	advice              advice
}

func slowHookMessages() slowHookText {
	return slowHookText{
		// title takes the hook and its average duration.
		title: text{
			usage.LocaleEnglish: "The %s hook takes %s on average",
			usage.LocaleKorean:  "%s 훅이 평균 %s 걸립니다",
		},
		// detail takes the number of runs.
		detail: text{
			usage.LocaleEnglish: "It ran %s times in the range. Work waits until a hook finishes, so its delay becomes waiting time.",
			usage.LocaleKorean:  "구간 안에서 %s회 실행됐습니다. 훅이 끝날 때까지 작업이 기다리므로 지연이 그대로 대기 시간이 됩니다.",
		},
		average: text{usage.LocaleEnglish: "Average duration", usage.LocaleKorean: "평균 실행 시간"},
		runs:    text{usage.LocaleEnglish: "Runs", usage.LocaleKorean: "실행 횟수"},
		peak:    text{usage.LocaleEnglish: "Longest run", usage.LocaleKorean: "가장 긴 실행 시간"},
		advice: advice{
			title: text{usage.LocaleEnglish: "Make the hook command lighter", usage.LocaleKorean: "훅 명령을 가볍게"},
			action: text{
				usage.LocaleEnglish: "Run heavy checks only on changed files, or move them to a background job.",
				usage.LocaleKorean:  "무거운 검사는 바뀐 파일에만 돌리거나 백그라운드 작업으로 옮깁니다.",
			},
		},
	}
}

func evalSlowHook(in usage.InsightInput) verdict {
	w, m := newWriter(in.Locale), slowHookMessages()
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
			w.millis(w.say(m.average), avg),
			w.count(w.say(m.runs), hook.Runs, unitRuns),
		}
		if hook.MaxMs != nil {
			evidence = append(evidence, w.millis(w.say(m.peak), *hook.MaxMs))
		}
		label := hookLabel(hook)
		v.findings = append(v.findings, finding{
			subject:        label,
			severity:       severity,
			title:          w.say(m.title, w.name(label), w.seconds(avg)),
			detail:         w.say(m.detail, formatCount(hook.Runs)),
			evidence:       evidence,
			recommendation: w.recommend(usage.RecommendHook, m.advice, ""),
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

// failingHookText is the message table of the failing hook rule.
type failingHookText struct {
	title, detail text
	failed, rate  text
	advice        advice
}

func failingHookMessages() failingHookText {
	return failingHookText{
		// title takes the hook and its failure rate in percent.
		title: text{
			usage.LocaleEnglish: "%[2]s%% of %[1]s hook runs failed",
			usage.LocaleKorean:  "%s 훅 실행의 %s%%가 실패했습니다",
		},
		// detail takes the number of runs and the number of failures.
		detail: text{
			usage.LocaleEnglish: "%[2]s of %[1]s runs failed. A hook that fails does not perform its check.",
			usage.LocaleKorean:  "실행 %s회 중 %s회가 실패했습니다. 실패한 훅은 맡은 검사를 하지 못합니다.",
		},
		failed: text{usage.LocaleEnglish: "Failed hook runs", usage.LocaleKorean: "실패한 훅 실행"},
		rate:   text{usage.LocaleEnglish: "Failure rate", usage.LocaleKorean: "실패율"},
		advice: advice{
			title: text{usage.LocaleEnglish: "Check the hook script", usage.LocaleKorean: "훅 스크립트 점검"},
			action: text{
				usage.LocaleEnglish: "Run the hook command in a terminal and check its exit code and error output.",
				usage.LocaleKorean:  "훅 명령을 터미널에서 직접 실행해 종료 코드와 오류 출력을 확인합니다.",
			},
		},
	}
}

func evalFailingHook(in usage.InsightInput) verdict {
	w, m := newWriter(in.Locale), failingHookMessages()
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
			title:    w.say(m.title, w.name(label), formatShare(hook.Failures, hook.Runs)),
			detail:   w.say(m.detail, formatCount(hook.Runs), formatCount(hook.Failures)),
			evidence: []usage.Evidence{
				w.countOf(w.say(m.failed), hook.Failures, hook.Runs, unitRuns),
				w.share(w.say(m.rate), hook.Failures, hook.Runs),
			},
			recommendation: w.recommend(usage.RecommendHook, m.advice, ""),
		})
	}
	return v
}
