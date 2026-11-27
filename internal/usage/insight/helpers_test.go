package insight

import (
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// wantInsight is what a rule test asserts about one fired insight.
type wantInsight struct {
	id         string
	severity   usage.Severity
	evidence   []usage.Evidence
	recKind    usage.RecommendationKind
	recTarget  string
	sessionIDs []string
}

// ruleCase is one input of a rule test with its expected outcome.
type ruleCase struct {
	name  string
	in    usage.InsightInput
	want  outcome
	items []wantInsight
}

// ev builds an expected evidence row holding a plain number.
func ev(label string, value float64, unit string) usage.Evidence {
	return usage.Evidence{Label: label, Value: value, Unit: unit}
}

// evOf builds an expected evidence row holding "value out of total".
func evOf(label string, value, total float64, unit string) usage.Evidence {
	return usage.Evidence{Label: label, Value: value, Unit: unit, Of: &total}
}

func ptr(v float64) *float64 { return &v }

// flatEvidence is Evidence with the denominator by value, so that two rows
// compare by content.
type flatEvidence struct {
	label string
	value float64
	unit  string
	of    float64
	hasOf bool
}

func flatten(evidence []usage.Evidence) []flatEvidence {
	flat := make([]flatEvidence, 0, len(evidence))
	for _, e := range evidence {
		row := flatEvidence{label: e.Label, value: e.Value, unit: e.Unit}
		if e.Of != nil {
			row.of, row.hasOf = *e.Of, true
		}
		flat = append(flat, row)
	}
	return flat
}

func findRule(t *testing.T, id ruleID) rule {
	t.Helper()
	for _, r := range rules() {
		if r.id == id {
			return r
		}
	}
	t.Fatalf("rule %q is not registered", id)
	return rule{}
}

// runRuleCases runs one rule over each case and compares the outcome and the
// asserted fields of every insight, in the order the rule produced them.
func runRuleCases(t *testing.T, id ruleID, cases []ruleCase) {
	t.Helper()
	r := findRule(t, id)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, items := r.run(tc.in)
			if got != tc.want {
				t.Fatalf("outcome = %d, want %d (items %+v)", got, tc.want, items)
			}
			if len(items) != len(tc.items) {
				t.Fatalf("got %d insights, want %d: %+v", len(items), len(tc.items), items)
			}
			for i, want := range tc.items {
				item := items[i]
				checkInsightShape(t, item)
				if item.ID != want.id {
					t.Errorf("item %d: ID = %q, want %q", i, item.ID, want.id)
				}
				if item.RuleID != string(id) {
					t.Errorf("item %d: RuleID = %q, want %q", i, item.RuleID, id)
				}
				if item.Severity != want.severity {
					t.Errorf("item %d: severity = %q, want %q", i, item.Severity, want.severity)
				}
				if got, want := flatten(item.Evidence), flatten(want.evidence); !slices.Equal(got, want) {
					t.Errorf("item %d: evidence = %+v, want %+v", i, got, want)
				}
				if item.Recommendation.Kind != want.recKind {
					t.Errorf("item %d: recommendation kind = %q, want %q", i, item.Recommendation.Kind, want.recKind)
				}
				if item.Recommendation.Target != want.recTarget {
					t.Errorf("item %d: recommendation target = %q, want %q", i, item.Recommendation.Target, want.recTarget)
				}
				if !slices.Equal(item.SessionIDs, want.sessionIDs) {
					t.Errorf("item %d: session IDs = %v, want %v", i, item.SessionIDs, want.sessionIDs)
				}
			}
		})
	}
}

// checkInsightShape asserts what every insight must satisfy regardless of
// its rule: identity, Korean formal text, evidence and a usable
// recommendation.
func checkInsightShape(t *testing.T, item usage.Insight) {
	t.Helper()
	if item.ID != item.RuleID && !strings.HasPrefix(item.ID, item.RuleID+subjectSeparator) {
		t.Errorf("%s: ID does not start with rule ID %q", item.ID, item.RuleID)
	}
	if !slices.Contains(validCategories(), item.Category) {
		t.Errorf("%s: unknown category %q", item.ID, item.Category)
	}
	if severityRank(item.Severity) == rankUnknown {
		t.Errorf("%s: unknown severity %q", item.ID, item.Severity)
	}
	if !hasHangul(item.Title) || !strings.HasSuffix(item.Title, "니다") {
		t.Errorf("%s: title %q is not a formal Korean sentence", item.ID, item.Title)
	}
	if !hasHangul(item.Detail) || !strings.HasSuffix(item.Detail, "니다.") {
		t.Errorf("%s: detail %q is not a formal Korean sentence", item.ID, item.Detail)
	}
	if len(item.Evidence) == 0 {
		t.Errorf("%s: no evidence", item.ID)
	}
	for _, e := range item.Evidence {
		if e.Label == "" || e.Unit == "" {
			t.Errorf("%s: evidence %+v lacks a label or a unit", item.ID, e)
		}
	}
	if len(item.SessionIDs) > maxSessionIDs {
		t.Errorf("%s: %d session IDs, at most %d allowed", item.ID, len(item.SessionIDs), maxSessionIDs)
	}
	rec := item.Recommendation
	if rec == nil {
		t.Fatalf("%s: no recommendation", item.ID)
	}
	if !slices.Contains(validKinds(), rec.Kind) {
		t.Errorf("%s: unknown recommendation kind %q", item.ID, rec.Kind)
	}
	if !hasHangul(rec.Title) || !hasHangul(rec.Action) || !strings.HasSuffix(rec.Action, "니다.") {
		t.Errorf("%s: recommendation %+v is not formal Korean text", item.ID, *rec)
	}
}

func validCategories() []usage.InsightCategory {
	return []usage.InsightCategory{
		usage.CategoryCost, usage.CategoryReliability, usage.CategoryWorkflow, usage.CategoryExtension,
	}
}

func validKinds() []usage.RecommendationKind {
	return []usage.RecommendationKind{
		usage.RecommendSkill, usage.RecommendPlugin, usage.RecommendMCP,
		usage.RecommendHook, usage.RecommendSetting, usage.RecommendHabit,
	}
}

func hasHangul(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Hangul, r) })
}

// Synthetic names used across the tests.
const (
	modelLarge = "model-large"
	modelSmall = "model-small"
)

func builtin(name string, calls, errors int64) usage.ToolUsage {
	return usage.ToolUsage{Name: name, Category: usage.ToolBuiltin, Calls: calls, Errors: errors}
}

func group(name string, calls, errors int64) usage.GroupUsage {
	return usage.GroupUsage{Name: name, Calls: calls, Errors: errors}
}

func program(name string, calls int64) usage.GroupUsage {
	return usage.GroupUsage{Name: name, Calls: calls}
}

// session builds a session row whose tokens add up to tokens.
func session(id string, tokens, compactions int64) usage.SessionRow {
	return usage.SessionRow{ID: id, Tokens: usage.TokenTotals{CacheRead: tokens}, Compactions: compactions}
}

// busyInput fires every rule at least once and covers all three severities.
func busyInput() usage.InsightInput {
	return usage.InsightInput{
		Overview: usage.Overview{
			Sessions:      25,
			ModelCalls:    300,
			CacheHitRatio: ptr(0.3),
			Tokens:        usage.TokenTotals{Input: 5000, Output: 1000, CacheRead: 3000, CacheCreate: 2000},
			Models:        []usage.ModelUsage{{Model: modelLarge, Calls: 300}},
		},
		Tools: usage.Tools{
			Tools: []usage.ToolUsage{
				builtin(usage.ToolNameBash, 400, 80),
				builtin(toolNameEdit, 50, 20),
				builtin(toolNameRead, 100, 0),
				{Name: "mcp__tracker__search", Category: usage.ToolMCP, MCPServer: "tracker", Calls: 30, Errors: 15},
			},
			MCPServers: []usage.GroupUsage{group("tracker", 30, 15)},
			Hooks: []usage.HookUsage{
				{Event: "Stop", Name: "Stop", Runs: 100, Failures: 30, AvgMs: ptr(1500), MaxMs: ptr(4000)},
				{Event: "PreToolUse", Name: "PreToolUse:Bash", Runs: 40, Failures: 4, AvgMs: ptr(2500)},
			},
			Programs: []usage.GroupUsage{
				program("cat", 80), program("grep", 60), program("gh", 70), program("git", 100), program("curl", 55),
			},
		},
		Sessions: []usage.SessionRow{
			session("s-01", 4000, 4), session("s-02", 1500, 2), session("s-03", 1200, 3),
			session("s-04", 900, 0), session("s-05", 800, 0), session("s-06", 700, 1),
		},
		Ext: []usage.ExtSourceRow{
			{Source: "deploy-script", Events: 50, Errors: 10, Rejected: 2},
			{Source: "lint-runner", Events: 40, Errors: 30},
		},
		ExtRejected: 5,
	}
}
