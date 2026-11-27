package insight

import (
	"reflect"
	"regexp"
	"slices"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// quietInput fires unused-extensions:mcp, which busyInput does not reach.
func quietInput() usage.InsightInput {
	return usage.InsightInput{
		Overview: usage.Overview{Sessions: 20},
	}
}

// TestEveryRuleWritesEveryLocale walks the registry and checks that every
// rule fires with complete text in every supported locale.
func TestEveryRuleWritesEveryLocale(t *testing.T) {
	for _, locale := range usage.Locales() {
		t.Run(string(locale), func(t *testing.T) {
			for _, r := range rules() {
				fired := 0
				for _, base := range []usage.InsightInput{busyInput(), quietInput()} {
					in := base
					in.Locale = locale
					result, items := r.run(in)
					if result != outcomeFired {
						continue
					}
					fired++
					for _, item := range items {
						checkInsightShape(t, item, locale)
					}
				}
				if fired == 0 {
					t.Errorf("rule %q never fired, so its %s text is unchecked", r.id, locale)
				}
			}
		})
	}
}

// TestLocaleChangesOnlyText checks that the locale leaves IDs, severities,
// numbers, targets and ordering alone.
func TestLocaleChangesOnlyText(t *testing.T) {
	report := func(locale usage.Locale) usage.InsightReport {
		in := busyInput()
		in.Locale = locale
		return Evaluate(in, fixedNow())
	}
	english := report(usage.LocaleEnglish)
	for _, locale := range usage.Locales() {
		other := report(locale)
		if !slices.Equal(itemIDs(other.Items), itemIDs(english.Items)) {
			t.Fatalf("%s order = %v, want %v", locale, itemIDs(other.Items), itemIDs(english.Items))
		}
		if other.RulesFired != english.RulesFired || other.RulesPassed != english.RulesPassed || other.RulesSkipped != english.RulesSkipped {
			t.Errorf("%s counts differ from English", locale)
		}
		for i, item := range other.Items {
			want := english.Items[i]
			if item.RuleID != want.RuleID || item.Severity != want.Severity || item.Category != want.Category {
				t.Errorf("%s %s: identity differs from English", locale, item.ID)
			}
			if !slices.Equal(item.SessionIDs, want.SessionIDs) {
				t.Errorf("%s %s: session IDs differ from English", locale, item.ID)
			}
			if item.Recommendation.Kind != want.Recommendation.Kind || item.Recommendation.Target != want.Recommendation.Target {
				t.Errorf("%s %s: recommendation kind or target differs from English", locale, item.ID)
			}
			if !slices.Equal(evidenceNumbers(item.Evidence), evidenceNumbers(want.Evidence)) {
				t.Errorf("%s %s: evidence numbers differ from English", locale, item.ID)
			}
		}
	}
}

// evidenceNumbers keeps the numbers of evidence rows and drops their words.
func evidenceNumbers(evidence []usage.Evidence) []flatEvidence {
	flat := flatten(evidence)
	for i := range flat {
		flat[i].label, flat[i].unit = "", ""
	}
	return flat
}

func TestDefaultLocaleIsEnglish(t *testing.T) {
	for _, locale := range []usage.Locale{"", "fr", "KO"} {
		in := busyInput()
		in.Locale = locale
		got := encode(t, Evaluate(in, fixedNow()))
		in.Locale = usage.LocaleEnglish
		if want := encode(t, Evaluate(in, fixedNow())); string(got) != string(want) {
			t.Errorf("locale %q does not fall back to English", locale)
		}
	}
}

// englishInsight is the full text of one English insight.
type englishInsight struct {
	title, detail, recTitle, recAction string
	evidence                           []usage.Evidence
}

func TestEnglishText(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   ruleID
		in   usage.InsightInput
		want englishInsight
	}{
		{
			name: "failing Bash",
			id:   ruleToolErrorRate,
			in:   usage.InsightInput{Tools: usage.Tools{Tools: []usage.ToolUsage{builtin(usage.ToolNameBash, 1200, 300)}}},
			want: englishInsight{
				title:     "25% of Bash calls failed",
				detail:    "300 of 1,200 calls failed. The session timeline shows the error of each failed call.",
				recTitle:  "Add frequent commands to the allowlist",
				recAction: "If many commands failed because permission was denied or the sandbox blocked them, add read-only commands to the allowlist and check the sandbox settings.",
				evidence:  []usage.Evidence{evOf("Failed Bash calls", 300, 1200, "calls"), ev("Failure rate", 25, "%")},
			},
		},
		{
			name: "slow hook",
			id:   ruleSlowHook,
			in:   hooksInput(usage.HookUsage{Event: "Stop", Name: "Stop", Runs: 198, AvgMs: ptr(1240), MaxMs: ptr(3000)}),
			want: englishInsight{
				title:     "The Stop hook takes 1.2 s on average",
				detail:    "It ran 198 times in the range. Work waits until a hook finishes, so its delay becomes waiting time.",
				recTitle:  "Make the hook command lighter",
				recAction: "Run heavy checks only on changed files, or move them to a background job.",
				evidence: []usage.Evidence{
					ev("Average duration", 1240, "ms"), ev("Runs", 198, "runs"), ev("Longest run", 3000, "ms"),
				},
			},
		},
		{
			name: "low cache hit",
			id:   ruleLowCacheHit,
			in: usage.InsightInput{Overview: usage.Overview{
				ModelCalls: 50, CacheHitRatio: ptr(0.4), Tokens: usage.TokenTotals{Input: 600, CacheRead: 400},
			}},
			want: englishInsight{
				title:     "The cache hit ratio is 40%",
				detail:    "This is the share of input tokens read from the cache over 50 model calls. Changing topics often within one session forces the cache to be rebuilt.",
				recTitle:  "Split sessions by topic",
				recAction: "Start a new session when the topic changes. The cache can be reused only while a session continues the same context.",
				evidence: []usage.Evidence{
					ev("Cache hit ratio", 40, "%"), evOf("Input tokens read from the cache", 400, 1000, "tokens"), ev("Model calls", 50, "calls"),
				},
			},
		},
		{
			name: "token concentration",
			id:   ruleTokenConcentration,
			in: usage.InsightInput{
				Overview: usage.Overview{Sessions: 5, Tokens: usage.TokenTotals{CacheRead: 1000}},
				Sessions: []usage.SessionRow{session("s-1", 300, 0), session("s-2", 250, 0), session("s-3", 100, 0)},
			},
			want: englishInsight{
				title:     "One session used 30% of the range's tokens",
				detail:    "2 of 5 sessions used 25% or more of the range's tokens. A long context may have carried on, or large outputs may have repeated.",
				recTitle:  "Open the session to find the cause",
				recAction: "In the session's token flow, find where the context suddenly grew and check which work used the tokens.",
				evidence: []usage.Evidence{
					evOf("Tokens of the largest session", 300, 1000, "tokens"),
					ev("Share of the largest session", 30, "%"),
					evOf("Sessions holding many tokens", 2, 5, "sessions"),
				},
			},
		},
		{
			name: "rejected input",
			id:   ruleExtRejected,
			in:   usage.InsightInput{ExtRejected: 3, Ext: []usage.ExtSourceRow{{Source: "", Events: 1, Rejected: 2}}},
			want: englishInsight{
				title:     "3 extension inputs were rejected",
				detail:    "Events in the wrong format are not stored. The rejection count is cumulative and does not follow the selected range.",
				recTitle:  "Check the sending format",
				recAction: "Compare the field names and value formats of the sending tool with the extension event API documentation.",
				evidence: []usage.Evidence{
					ev("Rejected inputs", 3, "inputs"),
					ev("Rejected from (unnamed)", 2, "inputs"),
					ev("Rejected from an unknown sender", 1, "inputs"),
				},
			},
		},
		{
			name: "repeated program",
			id:   ruleRepeatedProgram,
			in:   usage.InsightInput{Tools: usage.Tools{Programs: []usage.GroupUsage{program("gh", 120), program("git", 280)}}},
			want: englishInsight{
				title:     "The gh command ran 120 times",
				detail:    "That is 30% of 400 Bash calls. Calling the same external program again and again means building its arguments and parsing its output anew every time.",
				recTitle:  "Consider an MCP server for GitHub work",
				recAction: "Check whether repeated work such as looking up issues and pull requests can move to an MCP server for GitHub.",
				evidence:  []usage.Evidence{evOf("gh", 120, 400, "runs")},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.Locale = usage.LocaleEnglish
			result, items := findRule(t, tc.id).run(in)
			if result != outcomeFired || len(items) != 1 {
				t.Fatalf("outcome = %d with %d items, want one fired insight", result, len(items))
			}
			item := items[0]
			checkInsightShape(t, item, usage.LocaleEnglish)
			got := englishInsight{
				title: item.Title, detail: item.Detail,
				recTitle: item.Recommendation.Title, recAction: item.Recommendation.Action,
			}
			want := tc.want
			if got.title != want.title || got.detail != want.detail || got.recTitle != want.recTitle || got.recAction != want.recAction {
				t.Errorf("text =\n%+v\nwant\n%+v", got, want)
			}
			if !slices.Equal(flatten(item.Evidence), flatten(want.evidence)) {
				t.Errorf("evidence = %+v, want %+v", flatten(item.Evidence), flatten(want.evidence))
			}
		})
	}
}

// verbPattern matches one fmt verb, with or without an argument index.
var verbPattern = regexp.MustCompile(`%(\[\d+\])?[a-z]`)

// TestMessageTablesAreComplete checks every text of every message table:
// each supported locale has an entry, and all entries take the same number
// of arguments.
func TestMessageTablesAreComplete(t *testing.T) {
	tables := map[string]any{
		"common":              commonMessages(),
		"units":               unitTable(),
		"low-cache-hit":       lowCacheMessages(),
		"token-concentration": tokenConcentrationMessages(),
		"single-model":        singleModelMessages(),
		"slow-hook":           slowHookMessages(),
		"failing-hook":        failingHookMessages(),
		"tool-error-rate":     toolErrorMessages(),
		"mcp-server-error":    mcpErrorMessages(),
		"frequent-compaction": compactionMessages(),
		"bash-over-tools":     bashOverToolsMessages(),
		"repeated-program":    repeatedProgramMessages(),
		"program-advice":      programAdviceTable(),
		"extension-kinds":     extensionKinds(),
		"unused-extensions":   unusedExtMessages(),
		"ext-error-rate":      extErrorMessages(),
		"ext-rejected":        extRejectedMessages(),
	}
	textType := reflect.TypeFor[text]()
	for name, table := range tables {
		found := 0
		walkTexts(reflect.ValueOf(table), textType, func(v reflect.Value) {
			found++
			verbs := -1
			for _, locale := range usage.Locales() {
				entry := v.MapIndex(reflect.ValueOf(locale))
				if !entry.IsValid() || entry.String() == "" {
					t.Errorf("%s: a text has no %s entry", name, locale)
					continue
				}
				n := len(verbPattern.FindAllString(entry.String(), -1))
				if verbs >= 0 && n != verbs {
					t.Errorf("%s: %q takes %d arguments, other locales take %d", name, entry.String(), n, verbs)
				}
				verbs = n
			}
		})
		if found == 0 {
			t.Errorf("%s: no text found", name)
		}
	}
}

// walkTexts calls visit for every value of textType inside v.
func walkTexts(v reflect.Value, textType reflect.Type, visit func(reflect.Value)) {
	if v.Type() == textType {
		visit(v)
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			walkTexts(v.Field(i), textType, visit)
		}
	case reflect.Slice:
		for i := range v.Len() {
			walkTexts(v.Index(i), textType, visit)
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			walkTexts(iter.Value(), textType, visit)
		}
	}
}
