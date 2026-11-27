package insight

import (
	"slices"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func TestLowCacheHit(t *testing.T) {
	cache := func(calls int64, ratio *float64, tokens usage.TokenTotals) usage.InsightInput {
		return usage.InsightInput{Overview: usage.Overview{ModelCalls: calls, CacheHitRatio: ratio, Tokens: tokens}}
	}
	runRuleCases(t, ruleLowCacheHit, []ruleCase{
		{
			name: "fires with the token split",
			in:   cache(50, ptr(0.4), usage.TokenTotals{Input: 300, Output: 999, CacheRead: 400, CacheCreate: 300}),
			want: outcomeFired,
			items: []wantInsight{{
				id:       "low-cache-hit",
				severity: usage.SeverityInfo,
				evidence: []usage.Evidence{
					ev("캐시 적중률", 40, koPercent),
					evOf("캐시에서 읽은 입력 토큰", 400, 1000, koItems),
					ev("모델 호출", 50, koTimes),
				},
				recKind: usage.RecommendHabit,
			}},
		},
		{
			name: "fires without token totals",
			in:   cache(80, ptr(0.1234), usage.TokenTotals{}),
			want: outcomeFired,
			items: []wantInsight{{
				id:       "low-cache-hit",
				severity: usage.SeverityInfo,
				evidence: []usage.Evidence{ev("캐시 적중률", 12.3, koPercent), ev("모델 호출", 80, koTimes)},
				recKind:  usage.RecommendHabit,
			}},
		},
		{name: "passes at the threshold", in: cache(500, ptr(0.5), usage.TokenTotals{}), want: outcomePassed},
		{name: "is skipped below the call count", in: cache(49, ptr(0.1), usage.TokenTotals{}), want: outcomeSkipped},
		{name: "is skipped without a ratio", in: cache(500, nil, usage.TokenTotals{}), want: outcomeSkipped},
	})
}

func TestTokenConcentration(t *testing.T) {
	concentration := func(sessions, total int64, rows ...usage.SessionRow) usage.InsightInput {
		return usage.InsightInput{
			Overview: usage.Overview{
				Sessions: sessions,
				Tokens:   usage.TokenTotals{Input: total / 2, Output: total / 10, CacheRead: total / 5, CacheCreate: total / 5},
			},
			Sessions: rows,
		}
	}
	runRuleCases(t, ruleTokenConcentration, []ruleCase{
		{
			name: "fires and lists every session at the threshold",
			in: concentration(5, 1000,
				session("s-a", 250, 0), session("s-b", 300, 0), session("s-c", 249, 0), session("s-d", 100, 0)),
			want: outcomeFired,
			items: []wantInsight{{
				id:       "token-concentration",
				severity: usage.SeverityInfo,
				evidence: []usage.Evidence{
					evOf("가장 큰 세션의 토큰", 300, 1000, koItems),
					ev("가장 큰 세션의 비율", 30, koPercent),
					evOf("토큰이 몰린 세션", 2, 5, koItems),
				},
				recKind:    usage.RecommendHabit,
				sessionIDs: []string{"s-b", "s-a"},
			}},
		},
		{
			name: "keeps the share within the total when a session outgrows it",
			in:   concentration(9, 1000, session("s-a", 1200, 0), session("s-b", 100, 0)),
			want: outcomeFired,
			items: []wantInsight{{
				id:       "token-concentration",
				severity: usage.SeverityInfo,
				evidence: []usage.Evidence{
					evOf("가장 큰 세션의 토큰", 1200, 1200, koItems),
					ev("가장 큰 세션의 비율", 100, koPercent),
					evOf("토큰이 몰린 세션", 1, 9, koItems),
				},
				recKind:    usage.RecommendHabit,
				sessionIDs: []string{"s-a"},
			}},
		},
		{
			name: "passes when tokens are spread",
			in: concentration(5, 1000,
				session("s-a", 200, 0), session("s-b", 200, 0), session("s-c", 200, 0), session("s-d", 200, 0), session("s-e", 200, 0)),
			want: outcomePassed,
		},
		{name: "is skipped below the session count", in: concentration(4, 1000, session("s-a", 900, 0)), want: outcomeSkipped},
		{name: "is skipped without tokens", in: concentration(10, 0, session("s-a", 0, 0)), want: outcomeSkipped},
		{name: "is skipped without session rows", in: concentration(10, 1000), want: outcomeSkipped},
	})
}

func TestTokenConcentrationKeepsInputOrder(t *testing.T) {
	in := usage.InsightInput{
		Overview: usage.Overview{Sessions: 5, Tokens: usage.TokenTotals{Input: 1000}},
		Sessions: []usage.SessionRow{session("s-a", 100, 0), session("s-b", 600, 0), session("s-c", 300, 0)},
	}
	before := slices.Clone(in.Sessions)
	if got, _ := findRule(t, ruleTokenConcentration).run(in); got != outcomeFired {
		t.Fatalf("outcome = %d, want fired", got)
	}
	for i, row := range in.Sessions {
		if row.ID != before[i].ID {
			t.Fatalf("input sessions were reordered: got %q at %d, want %q", row.ID, i, before[i].ID)
		}
	}
}

func TestSingleModel(t *testing.T) {
	models := func(rows ...usage.ModelUsage) usage.InsightInput {
		var calls int64
		for _, row := range rows {
			calls += row.Calls
		}
		return usage.InsightInput{Overview: usage.Overview{ModelCalls: calls, Models: rows}}
	}
	fired := func(calls float64) []wantInsight {
		return []wantInsight{{
			id:        "single-model",
			severity:  usage.SeverityInfo,
			evidence:  []usage.Evidence{evOf(modelLarge+" 호출", calls, calls, koTimes), ev("사용한 모델", 1, koItems)},
			recKind:   usage.RecommendSetting,
			recTarget: settingModel,
		}}
	}
	runRuleCases(t, ruleSingleModel, []ruleCase{
		{
			name:  "fires at the call threshold",
			in:    models(usage.ModelUsage{Model: modelLarge, Calls: 200}),
			want:  outcomeFired,
			items: fired(200),
		},
		{
			name: "ignores placeholder and unnamed rows",
			in: models(
				usage.ModelUsage{Model: modelLarge, Calls: 250},
				usage.ModelUsage{Model: "<placeholder>", Calls: 3},
				usage.ModelUsage{Model: "", Calls: 2},
				usage.ModelUsage{Model: modelSmall, Calls: 0},
			),
			want:  outcomeFired,
			items: fired(250),
		},
		{
			name: "passes with two models",
			in:   models(usage.ModelUsage{Model: modelLarge, Calls: 150}, usage.ModelUsage{Model: modelSmall, Calls: 50}),
			want: outcomePassed,
		},
		{name: "is skipped below the call count", in: models(usage.ModelUsage{Model: modelLarge, Calls: 199}), want: outcomeSkipped},
		{
			name: "is skipped without a model breakdown",
			in:   usage.InsightInput{Overview: usage.Overview{ModelCalls: 500}},
			want: outcomeSkipped,
		},
	})
}
