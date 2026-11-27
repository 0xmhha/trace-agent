package insight

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Low cache hit ratio: with enough model calls, less than half of the input
// tokens came from the cache.
//
// Decision: the ratio is the one the overview reports. When it is absent the
// range has no input tokens and the rule is skipped.
const (
	lowCacheMinModelCalls int64   = 50
	lowCacheHitRatio      float64 = 0.5
)

func evalLowCacheHit(in usage.InsightInput) verdict {
	overview := in.Overview
	if overview.ModelCalls < lowCacheMinModelCalls || overview.CacheHitRatio == nil {
		return verdict{}
	}
	ratio := *overview.CacheHitRatio
	if ratio >= lowCacheHitRatio {
		return verdict{judged: true}
	}
	hitPct := roundPercent(ratio * percentScale)
	evidence := []usage.Evidence{{Label: "캐시 적중률", Value: hitPct, Unit: unitPercent}}
	tokens := overview.Tokens
	if inputSide := tokens.Input + tokens.CacheRead + tokens.CacheCreate; inputSide > 0 {
		evidence = append(evidence, countOf("캐시에서 읽은 입력 토큰", tokens.CacheRead, inputSide, unitItems))
	}
	evidence = append(evidence, count("모델 호출", overview.ModelCalls, unitTimes))
	return verdict{judged: true, findings: []finding{{
		severity: usage.SeverityInfo,
		title:    fmt.Sprintf("캐시 적중률이 %s%%입니다", formatPercent(hitPct)),
		detail: fmt.Sprintf("모델 호출 %s회의 입력 토큰 가운데 캐시에서 읽은 비율입니다. 한 세션에서 주제가 자주 바뀌면 캐시를 다시 만들게 됩니다.",
			formatCount(overview.ModelCalls)),
		evidence: evidence,
		recommendation: &usage.Recommendation{
			Kind:   usage.RecommendHabit,
			Title:  "세션을 주제별로 나누기",
			Action: "주제가 바뀌면 새 세션을 시작합니다. 한 세션에서는 같은 맥락을 이어 가야 캐시를 다시 쓸 수 있습니다.",
		},
	}}}
}

// Token concentration: one session used a quarter or more of the range's
// tokens.
//
// Decisions:
//   - Tokens are the sum of input, output, cache-read and cache-create, for
//     the session and for the range alike.
//   - The rule fires once. SessionIDs lists every session at or over the
//     threshold, heaviest first; the evidence describes the heaviest.
//   - The session list and the overview come from separate queries, so the
//     heaviest session may briefly exceed the range total while a scan runs.
//     The total is then raised to that session so the share stays within
//     100%.
const (
	tokenShareMinSessions int64 = 5
	tokenSharePct         int64 = 25
)

func evalTokenConcentration(in usage.InsightInput) verdict {
	total := totalTokens(in.Overview.Tokens)
	if in.Overview.Sessions < tokenShareMinSessions || total <= 0 || len(in.Sessions) == 0 {
		return verdict{}
	}
	sessions := slices.Clone(in.Sessions)
	slices.SortStableFunc(sessions, func(a, b usage.SessionRow) int {
		return cmp.Or(
			cmp.Compare(totalTokens(b.Tokens), totalTokens(a.Tokens)),
			cmp.Compare(a.ID, b.ID),
		)
	})
	heaviest := totalTokens(sessions[0].Tokens)
	total = max(total, heaviest)
	var heavy []string
	for _, session := range sessions {
		if !atLeastPercent(totalTokens(session.Tokens), total, tokenSharePct) {
			break
		}
		heavy = append(heavy, session.ID)
	}
	if len(heavy) == 0 {
		return verdict{judged: true}
	}
	return verdict{judged: true, findings: []finding{{
		severity: usage.SeverityInfo,
		title:    fmt.Sprintf("한 세션이 구간 토큰의 %s%%를 썼습니다", formatShare(heaviest, total)),
		detail: fmt.Sprintf("구간 토큰의 %d%% 이상을 쓴 세션이 %s개 가운데 %d개입니다. 긴 맥락이 이어졌거나 큰 출력이 되풀이됐을 수 있습니다.",
			tokenSharePct, formatCount(in.Overview.Sessions), len(heavy)),
		evidence: []usage.Evidence{
			countOf("가장 큰 세션의 토큰", heaviest, total, unitItems),
			share("가장 큰 세션의 비율", heaviest, total),
			countOf("토큰이 몰린 세션", int64(len(heavy)), in.Overview.Sessions, unitItems),
		},
		sessionIDs: heavy,
		recommendation: &usage.Recommendation{
			Kind:   usage.RecommendHabit,
			Title:  "해당 세션을 열어 원인 확인",
			Action: "세션 상세의 토큰 흐름에서 맥락이 갑자기 커진 지점을 찾아 어떤 작업이 토큰을 썼는지 확인합니다.",
		},
	}}}
}

// Single model: every model call of a busy range went to one model.
//
// Decisions:
//   - A model ID in angle brackets is a placeholder the client writes for a
//     response it produced itself, not a model. Such rows and rows without an
//     ID are left out, so a placeholder never hides single-model use.
//   - The sample is the number of calls in the model breakdown. Without a
//     breakdown the rule is skipped.
const singleModelMinCalls int64 = 200

// placeholderModelPrefix starts a model ID that names no real model.
const placeholderModelPrefix = "<"

// settingModel is the settings key that selects the model.
const settingModel = "model"

func evalSingleModel(in usage.InsightInput) verdict {
	var (
		calls  int64
		models []usage.ModelUsage
	)
	for _, model := range in.Overview.Models {
		if model.Model == "" || strings.HasPrefix(model.Model, placeholderModelPrefix) || model.Calls <= 0 {
			continue
		}
		calls += model.Calls
		models = append(models, model)
	}
	if calls < singleModelMinCalls {
		return verdict{}
	}
	if len(models) != 1 {
		return verdict{judged: true}
	}
	only := models[0].Model
	return verdict{judged: true, findings: []finding{{
		severity: usage.SeverityInfo,
		title:    "모든 모델 호출이 한 모델에 몰려 있습니다",
		detail: fmt.Sprintf("모델 호출 %s회를 %s 모델 하나로 처리했습니다. 검색이나 요약 같은 단순 작업은 가벼운 모델로도 충분할 수 있습니다.",
			formatCount(calls), only),
		evidence: []usage.Evidence{
			countOf(only+" 호출", calls, calls, unitTimes),
			count("사용한 모델", 1, unitItems),
		},
		recommendation: &usage.Recommendation{
			Kind:   usage.RecommendSetting,
			Title:  "단순 작업에 가벼운 모델 검토",
			Action: "하위 에이전트나 단순한 작업에 더 가벼운 모델을 지정할 수 있는지 검토합니다.",
			Target: settingModel,
		},
	}}}
}
