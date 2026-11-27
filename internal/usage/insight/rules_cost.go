package insight

import (
	"cmp"
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

// lowCacheText is the message table of the low cache hit rule.
type lowCacheText struct {
	title, detail              text
	hitRatio, cacheRead, calls text
	advice                     advice
}

func lowCacheMessages() lowCacheText {
	return lowCacheText{
		// title takes the hit ratio in percent.
		title: text{
			usage.LocaleEnglish: "The cache hit ratio is %s%%",
			usage.LocaleKorean:  "캐시 적중률이 %s%%입니다",
		},
		// detail takes the number of model calls.
		detail: text{
			usage.LocaleEnglish: "This is the share of input tokens read from the cache over %s model calls. Changing topics often within one session forces the cache to be rebuilt.",
			usage.LocaleKorean:  "모델 호출 %s회의 입력 토큰 가운데 캐시에서 읽은 비율입니다. 한 세션에서 주제가 자주 바뀌면 캐시를 다시 만들게 됩니다.",
		},
		hitRatio:  text{usage.LocaleEnglish: "Cache hit ratio", usage.LocaleKorean: "캐시 적중률"},
		cacheRead: text{usage.LocaleEnglish: "Input tokens read from the cache", usage.LocaleKorean: "캐시에서 읽은 입력 토큰"},
		calls:     text{usage.LocaleEnglish: "Model calls", usage.LocaleKorean: "모델 호출"},
		advice: advice{
			title: text{usage.LocaleEnglish: "Split sessions by topic", usage.LocaleKorean: "세션을 주제별로 나누기"},
			action: text{
				usage.LocaleEnglish: "Start a new session when the topic changes. The cache can be reused only while a session continues the same context.",
				usage.LocaleKorean:  "주제가 바뀌면 새 세션을 시작합니다. 한 세션에서는 같은 맥락을 이어 가야 캐시를 다시 쓸 수 있습니다.",
			},
		},
	}
}

func evalLowCacheHit(in usage.InsightInput) verdict {
	overview := in.Overview
	if overview.ModelCalls < lowCacheMinModelCalls || overview.CacheHitRatio == nil {
		return verdict{}
	}
	ratio := *overview.CacheHitRatio
	if ratio >= lowCacheHitRatio {
		return verdict{judged: true}
	}
	w, m := newWriter(in.Locale), lowCacheMessages()
	hitPct := roundPercent(ratio * percentScale)
	evidence := []usage.Evidence{w.percent(w.say(m.hitRatio), hitPct)}
	tokens := overview.Tokens
	if inputSide := tokens.Input + tokens.CacheRead + tokens.CacheCreate; inputSide > 0 {
		evidence = append(evidence, w.countOf(w.say(m.cacheRead), tokens.CacheRead, inputSide, unitTokens))
	}
	evidence = append(evidence, w.count(w.say(m.calls), overview.ModelCalls, unitCalls))
	return verdict{judged: true, findings: []finding{{
		severity:       usage.SeverityInfo,
		title:          w.say(m.title, formatPercent(hitPct)),
		detail:         w.say(m.detail, formatCount(overview.ModelCalls)),
		evidence:       evidence,
		recommendation: w.recommend(usage.RecommendHabit, m.advice, ""),
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

// tokenConcentrationText is the message table of the token concentration
// rule.
type tokenConcentrationText struct {
	title, detail                                text
	heaviestTokens, heaviestShare, heavySessions text
	advice                                       advice
}

func tokenConcentrationMessages() tokenConcentrationText {
	return tokenConcentrationText{
		// title takes the heaviest session's share in percent.
		title: text{
			usage.LocaleEnglish: "One session used %s%% of the range's tokens",
			usage.LocaleKorean:  "한 세션이 구간 토큰의 %s%%를 썼습니다",
		},
		// detail takes the threshold in percent, the number of sessions and
		// the number of heavy sessions.
		detail: text{
			usage.LocaleEnglish: "%[3]d of %[2]s sessions used %[1]d%% or more of the range's tokens. A long context may have carried on, or large outputs may have repeated.",
			usage.LocaleKorean:  "구간 토큰의 %d%% 이상을 쓴 세션이 %s개 가운데 %d개입니다. 긴 맥락이 이어졌거나 큰 출력이 되풀이됐을 수 있습니다.",
		},
		heaviestTokens: text{usage.LocaleEnglish: "Tokens of the largest session", usage.LocaleKorean: "가장 큰 세션의 토큰"},
		heaviestShare:  text{usage.LocaleEnglish: "Share of the largest session", usage.LocaleKorean: "가장 큰 세션의 비율"},
		heavySessions:  text{usage.LocaleEnglish: "Sessions holding many tokens", usage.LocaleKorean: "토큰이 몰린 세션"},
		advice: advice{
			title: text{usage.LocaleEnglish: "Open the session to find the cause", usage.LocaleKorean: "해당 세션을 열어 원인 확인"},
			action: text{
				usage.LocaleEnglish: "In the session's token flow, find where the context suddenly grew and check which work used the tokens.",
				usage.LocaleKorean:  "세션 상세의 토큰 흐름에서 맥락이 갑자기 커진 지점을 찾아 어떤 작업이 토큰을 썼는지 확인합니다.",
			},
		},
	}
}

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
	w, m := newWriter(in.Locale), tokenConcentrationMessages()
	return verdict{judged: true, findings: []finding{{
		severity: usage.SeverityInfo,
		title:    w.say(m.title, formatShare(heaviest, total)),
		detail:   w.say(m.detail, tokenSharePct, formatCount(in.Overview.Sessions), len(heavy)),
		evidence: []usage.Evidence{
			w.countOf(w.say(m.heaviestTokens), heaviest, total, unitTokens),
			w.share(w.say(m.heaviestShare), heaviest, total),
			w.countOf(w.say(m.heavySessions), int64(len(heavy)), in.Overview.Sessions, unitSessions),
		},
		sessionIDs:     heavy,
		recommendation: w.recommend(usage.RecommendHabit, m.advice, ""),
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

// singleModelText is the message table of the single model rule.
type singleModelText struct {
	title, detail          text
	modelCalls, modelsUsed text
	advice                 advice
}

func singleModelMessages() singleModelText {
	return singleModelText{
		title: text{
			usage.LocaleEnglish: "All model calls went to one model",
			usage.LocaleKorean:  "모든 모델 호출이 한 모델에 몰려 있습니다",
		},
		// detail takes the number of calls and the model ID.
		detail: text{
			usage.LocaleEnglish: "All %s model calls were handled by %s. Simple work such as searching or summarizing may do fine with a lighter model.",
			usage.LocaleKorean:  "모델 호출 %s회를 %s 모델 하나로 처리했습니다. 검색이나 요약 같은 단순 작업은 가벼운 모델로도 충분할 수 있습니다.",
		},
		// modelCalls takes the model ID.
		modelCalls: text{usage.LocaleEnglish: "%s calls", usage.LocaleKorean: "%s 호출"},
		modelsUsed: text{usage.LocaleEnglish: "Models used", usage.LocaleKorean: "사용한 모델"},
		advice: advice{
			title: text{usage.LocaleEnglish: "Consider a lighter model for simple work", usage.LocaleKorean: "단순 작업에 가벼운 모델 검토"},
			action: text{
				usage.LocaleEnglish: "Check whether sub-agents or simple tasks can be assigned a lighter model.",
				usage.LocaleKorean:  "하위 에이전트나 단순한 작업에 더 가벼운 모델을 지정할 수 있는지 검토합니다.",
			},
		},
	}
}

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
	w, m := newWriter(in.Locale), singleModelMessages()
	return verdict{judged: true, findings: []finding{{
		severity: usage.SeverityInfo,
		title:    w.say(m.title),
		detail:   w.say(m.detail, formatCount(calls), only),
		evidence: []usage.Evidence{
			w.countOf(w.say(m.modelCalls, only), calls, calls, unitCalls),
			w.count(w.say(m.modelsUsed), 1, unitModels),
		},
		recommendation: w.recommend(usage.RecommendSetting, m.advice, settingModel),
	}}}
}
