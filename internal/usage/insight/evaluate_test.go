package insight

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// ruleCount is the number of rules in the design's rule table.
const ruleCount = 13

func fixedNow() time.Time {
	return time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
}

func encode(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func itemIDs(items []usage.Insight) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func TestRegistry(t *testing.T) {
	kebab := regexp.MustCompile(`^[a-z]+(-[a-z]+)*$`)
	registered := rules()
	if len(registered) != ruleCount {
		t.Fatalf("registry holds %d rules, want %d", len(registered), ruleCount)
	}
	var seen []ruleID
	for _, r := range registered {
		if !kebab.MatchString(string(r.id)) {
			t.Errorf("rule ID %q is not kebab-case", r.id)
		}
		if slices.Contains(seen, r.id) {
			t.Errorf("rule ID %q is registered twice", r.id)
		}
		seen = append(seen, r.id)
		if r.eval == nil {
			t.Errorf("rule %q has no evaluation function", r.id)
		}
		if !slices.Contains(validCategories(), r.category) {
			t.Errorf("rule %q has unknown category %q", r.id, r.category)
		}
	}
}

func TestEvaluateEmptyInput(t *testing.T) {
	report := Evaluate(usage.InsightInput{}, fixedNow())
	if report.Items == nil || len(report.Items) != 0 {
		t.Fatalf("Items = %#v, want empty and non-nil", report.Items)
	}
	if report.RulesFired != 0 || report.RulesPassed != 0 || report.RulesSkipped != ruleCount {
		t.Fatalf("fired/passed/skipped = %d/%d/%d, want 0/0/%d",
			report.RulesFired, report.RulesPassed, report.RulesSkipped, ruleCount)
	}
	if !bytes.Contains(encode(t, report), []byte(`"items":[]`)) {
		t.Fatalf("empty items must encode as [], got %s", encode(t, report))
	}
}

func TestEvaluateOrdersBySeverityThenRuleThenID(t *testing.T) {
	report := Evaluate(busyInput(), fixedNow())
	want := []string{
		// critical
		"ext-error-rate:lint-runner",
		"failing-hook:Stop",
		"mcp-server-error-rate:tracker",
		"slow-hook:PreToolUse:Bash",
		"tool-error-rate:Edit",
		// warn
		"ext-error-rate:deploy-script",
		"ext-rejected",
		"failing-hook:PreToolUse:Bash",
		"slow-hook:Stop",
		"tool-error-rate:Bash",
		// info
		"bash-instead-of-tools",
		"frequent-compaction",
		"low-cache-hit",
		"repeated-external-program:curl",
		"repeated-external-program:gh",
		"single-model",
		"token-concentration",
		"unused-extensions:agent",
		"unused-extensions:skill",
	}
	if got := itemIDs(report.Items); !slices.Equal(got, want) {
		t.Fatalf("order =\n%v\nwant\n%v", got, want)
	}
	if report.RulesFired != ruleCount || report.RulesPassed != 0 || report.RulesSkipped != 0 {
		t.Fatalf("fired/passed/skipped = %d/%d/%d, want %d/0/0",
			report.RulesFired, report.RulesPassed, report.RulesSkipped, ruleCount)
	}
	for i, item := range report.Items {
		checkInsightShape(t, item)
		if i > 0 && severityRank(report.Items[i-1].Severity) > severityRank(item.Severity) {
			t.Errorf("item %d (%s) is more severe than the one before it", i, item.ID)
		}
	}
}

func TestEvaluateCountsRulesNotInsights(t *testing.T) {
	in := usage.InsightInput{
		Overview: usage.Overview{
			Sessions:      20,
			ModelCalls:    60,
			CacheHitRatio: ptr(0.9),
			Tokens:        usage.TokenTotals{Input: 1000},
			Models:        []usage.ModelUsage{{Model: modelLarge, Calls: 60}},
		},
		Tools: usage.Tools{
			Tools:    []usage.ToolUsage{builtin(usage.ToolNameBash, 100, 0)},
			Programs: []usage.GroupUsage{program("cat", 5)},
		},
		Sessions: []usage.SessionRow{session("s-a", 100, 0), session("s-b", 100, 0), session("s-c", 100, 0)},
	}
	report := Evaluate(in, fixedNow())
	// unused-extensions fires three insights and counts as one fired rule.
	wantIDs := []string{"unused-extensions:agent", "unused-extensions:mcp", "unused-extensions:skill"}
	if got := itemIDs(report.Items); !slices.Equal(got, wantIDs) {
		t.Fatalf("items = %v, want %v", got, wantIDs)
	}
	// Passed: tool-error-rate, low-cache-hit, frequent-compaction,
	// bash-instead-of-tools, repeated-external-program, token-concentration.
	// Skipped: mcp-server-error-rate, slow-hook, failing-hook, single-model,
	// ext-error-rate, ext-rejected.
	if report.RulesFired != 1 || report.RulesPassed != 6 || report.RulesSkipped != 6 {
		t.Fatalf("fired/passed/skipped = %d/%d/%d, want 1/6/6",
			report.RulesFired, report.RulesPassed, report.RulesSkipped)
	}
	if total := report.RulesFired + report.RulesPassed + report.RulesSkipped; total != ruleCount {
		t.Fatalf("counted %d rules, want %d", total, ruleCount)
	}
}

func TestEvaluateIsDeterministic(t *testing.T) {
	first := encode(t, Evaluate(busyInput(), fixedNow()))
	second := encode(t, Evaluate(busyInput(), fixedNow()))
	if !bytes.Equal(first, second) {
		t.Fatalf("two runs over the same input differ:\n%s\n%s", first, second)
	}

	// The order of the aggregate rows must not leak into the report.
	shuffled := busyInput()
	slices.Reverse(shuffled.Tools.Tools)
	slices.Reverse(shuffled.Tools.Hooks)
	slices.Reverse(shuffled.Tools.Programs)
	slices.Reverse(shuffled.Sessions)
	slices.Reverse(shuffled.Ext)
	if reordered := encode(t, Evaluate(shuffled, fixedNow())); !bytes.Equal(first, reordered) {
		t.Fatalf("row order changed the report:\n%s\n%s", first, reordered)
	}
}

func TestEvaluateDoesNotChangeInput(t *testing.T) {
	in := busyInput()
	before := encode(t, in)
	Evaluate(in, fixedNow())
	if after := encode(t, in); !bytes.Equal(before, after) {
		t.Fatalf("input changed:\n%s\n%s", before, after)
	}
}

func TestEvaluateCopiesCoverageAndStamp(t *testing.T) {
	scanned := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		coverage usage.Coverage
	}{
		{"complete", usage.Coverage{State: usage.CoverageComplete, FilesTracked: 4, LastScanAt: scanned, Reasons: []string{}}},
		{"partial", usage.Coverage{
			State: usage.CoveragePartial, FilesTracked: 9, FilesUnreadable: 1, LinesSkipped: 3,
			Reasons: []string{usage.ReasonUnreadableFiles, usage.ReasonSkippedLines},
		}},
		{"zero", usage.Coverage{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := busyInput()
			in.Overview.Coverage = tc.coverage
			report := Evaluate(in, fixedNow())
			if !bytes.Equal(encode(t, report.Coverage), encode(t, tc.coverage)) {
				t.Errorf("coverage = %+v, want %+v", report.Coverage, tc.coverage)
			}
			if !report.GeneratedAt.Equal(fixedNow()) {
				t.Errorf("GeneratedAt = %v, want %v", report.GeneratedAt, fixedNow())
			}
		})
	}
}

func TestSeverityRank(t *testing.T) {
	order := []usage.Severity{usage.SeverityCritical, usage.SeverityWarn, usage.SeverityInfo, usage.Severity("other")}
	for i := 1; i < len(order); i++ {
		if severityRank(order[i-1]) >= severityRank(order[i]) {
			t.Errorf("%q must rank before %q", order[i-1], order[i])
		}
	}
}

func TestInsightID(t *testing.T) {
	for _, tc := range []struct {
		name, subject, want string
	}{
		{"without a subject", "", "tool-error-rate"},
		{"with a subject", "Bash", "tool-error-rate:Bash"},
		{"subject with a separator", "PreToolUse:Bash", "tool-error-rate:PreToolUse:Bash"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := insightID(ruleToolErrorRate, tc.subject); got != tc.want {
				t.Fatalf("insightID = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunFillsEmptyEvidenceAndCapsSessions(t *testing.T) {
	r := rule{
		id:       "sample-rule",
		category: usage.CategoryWorkflow,
		eval: func(usage.InsightInput) verdict {
			return verdict{judged: true, findings: []finding{{
				severity:   usage.SeverityInfo,
				sessionIDs: []string{"a", "b", "c", "d", "e", "f", "g"},
			}}}
		},
	}
	got, items := r.run(usage.InsightInput{})
	if got != outcomeFired || len(items) != 1 {
		t.Fatalf("outcome = %d with %d items, want fired with 1", got, len(items))
	}
	if items[0].Evidence == nil {
		t.Error("Evidence must not be nil")
	}
	if len(items[0].SessionIDs) != maxSessionIDs {
		t.Errorf("got %d session IDs, want %d", len(items[0].SessionIDs), maxSessionIDs)
	}
	if items[0].Category != usage.CategoryWorkflow || items[0].RuleID != "sample-rule" {
		t.Errorf("rule identity was not stamped: %+v", items[0])
	}
}
