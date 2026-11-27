package insight

import (
	"slices"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func TestFrequentCompaction(t *testing.T) {
	sessions := func(rows ...usage.SessionRow) usage.InsightInput {
		return usage.InsightInput{Sessions: rows}
	}
	runRuleCases(t, ruleFrequentCompaction, []ruleCase{
		{
			name: "fires and lists the most compacted sessions first",
			in:   sessions(session("s-2", 10, 2), session("s-5", 10, 5), session("s-3", 10, 3), session("s-0", 10, 0)),
			want: outcomeFired,
			items: []wantInsight{{
				id:       "frequent-compaction",
				severity: usage.SeverityInfo,
				evidence: []usage.Evidence{
					evOf("압축이 2회 이상인 세션", 3, 4, koItems),
					ev("한 세션의 최대 압축 횟수", 5, koTimes),
				},
				recKind:    usage.RecommendHabit,
				sessionIDs: []string{"s-5", "s-3", "s-2"},
			}},
		},
		{
			name: "points at no more than five sessions",
			in: sessions(
				session("s-g", 10, 2), session("s-f", 10, 2), session("s-e", 10, 2), session("s-d", 10, 2),
				session("s-c", 10, 2), session("s-b", 10, 2), session("s-a", 10, 9)),
			want: outcomeFired,
			items: []wantInsight{{
				id:       "frequent-compaction",
				severity: usage.SeverityInfo,
				evidence: []usage.Evidence{
					evOf("압축이 2회 이상인 세션", 7, 7, koItems),
					ev("한 세션의 최대 압축 횟수", 9, koTimes),
				},
				recKind:    usage.RecommendHabit,
				sessionIDs: []string{"s-a", "s-b", "s-c", "s-d", "s-e"},
			}},
		},
		{
			name: "passes with too few compacted sessions",
			in:   sessions(session("s-a", 10, 2), session("s-b", 10, 2), session("s-c", 10, 1)),
			want: outcomePassed,
		},
		{
			name: "is skipped with fewer sessions than it needs",
			in:   sessions(session("s-a", 10, 9), session("s-b", 10, 9)),
			want: outcomeSkipped,
		},
		{name: "is skipped without sessions", in: usage.InsightInput{}, want: outcomeSkipped},
	})
}

func bashInput(bashCalls int64, programs ...usage.GroupUsage) usage.InsightInput {
	in := usage.InsightInput{Tools: usage.Tools{Programs: programs}}
	if bashCalls > 0 {
		in.Tools.Tools = []usage.ToolUsage{builtin(usage.ToolNameBash, bashCalls, 0)}
	}
	return in
}

func TestBashOverTools(t *testing.T) {
	habit := func(evidence ...usage.Evidence) []wantInsight {
		return []wantInsight{{
			id:       "bash-instead-of-tools",
			severity: usage.SeverityInfo,
			evidence: evidence,
			recKind:  usage.RecommendHabit,
		}}
	}
	runRuleCases(t, ruleBashOverTools, []ruleCase{
		{
			name: "fires and names the most used programs",
			in: bashInput(100,
				program("sed", 5), program("ls", 10), program("git", 50), program("grep", 15), program("cat", 20)),
			want: outcomeFired,
			items: habit(
				evOf("파일 읽기와 검색 명령", 50, 100, koTimes),
				ev("Bash 호출 중 비율", 50, koPercent),
				ev("cat 실행", 20, koTimes),
				ev("grep 실행", 15, koTimes),
				ev("ls 실행", 10, koTimes),
			),
		},
		{
			name: "fires exactly on the share threshold",
			in:   bashInput(200, program("tail", 30), program("head", 30)),
			want: outcomeFired,
			items: habit(
				evOf("파일 읽기와 검색 명령", 60, 200, koTimes),
				ev("Bash 호출 중 비율", 30, koPercent),
				ev("head 실행", 30, koTimes),
				ev("tail 실행", 30, koTimes),
			),
		},
		{
			name: "falls back to program rows without a Bash tool row",
			in:   bashInput(0, program("find", 50), program("git", 50)),
			want: outcomeFired,
			items: habit(
				evOf("파일 읽기와 검색 명령", 50, 100, koTimes),
				ev("Bash 호출 중 비율", 50, koPercent),
				ev("find 실행", 50, koTimes),
			),
		},
		{name: "passes when the share is low", in: bashInput(1000, program("cat", 60)), want: outcomePassed},
		{name: "passes when the count is low", in: bashInput(100, program("cat", 49)), want: outcomePassed},
		{name: "is skipped with too few Bash calls", in: bashInput(49, program("cat", 49)), want: outcomeSkipped},
		{name: "is skipped without Bash calls", in: usage.InsightInput{}, want: outcomeSkipped},
	})
}

func TestRepeatedProgram(t *testing.T) {
	runRuleCases(t, ruleRepeatedProgram, []ruleCase{
		{
			name: "fires once per known program at the threshold",
			in: bashInput(400,
				program("gh", 120), program("git", 100), program("curl", 50), program("kubectl", 49)),
			want: outcomeFired,
			items: []wantInsight{
				{
					id:       "repeated-external-program:gh",
					severity: usage.SeverityInfo,
					evidence: []usage.Evidence{evOf("gh 실행", 120, 400, koTimes)},
					recKind:  usage.RecommendMCP,
				},
				{
					id:       "repeated-external-program:curl",
					severity: usage.SeverityInfo,
					evidence: []usage.Evidence{evOf("curl 실행", 50, 400, koTimes)},
					recKind:  usage.RecommendSkill,
				},
			},
		},
		{name: "passes below the call count", in: bashInput(300, program("git", 250), program("gh", 49)), want: outcomePassed},
		{name: "passes for a program outside the table", in: bashInput(500, program("make", 500)), want: outcomePassed},
		{name: "is skipped with too few Bash calls", in: bashInput(49, program("gh", 49)), want: outcomeSkipped},
		{name: "is skipped without Bash calls", in: usage.InsightInput{}, want: outcomeSkipped},
	})
}

func TestProgramAdviceTable(t *testing.T) {
	var seen []string
	for _, entry := range programAdviceTable() {
		if len(entry.programs) == 0 {
			t.Errorf("entry %q lists no program", entry.advice.title[usage.LocaleEnglish])
		}
		if !slices.Contains(validKinds(), entry.kind) {
			t.Errorf("entry %q has unknown kind %q", entry.advice.title[usage.LocaleEnglish], entry.kind)
		}
		if !hasHangul(entry.advice.title[usage.LocaleKorean]) || !hasHangul(entry.advice.action[usage.LocaleKorean]) {
			t.Errorf("entry %q lacks Korean text", entry.advice.title[usage.LocaleEnglish])
		}
		for _, name := range entry.programs {
			if slices.Contains(seen, name) {
				t.Errorf("program %q is listed twice", name)
			}
			seen = append(seen, name)
		}
	}
	for _, name := range []string{"gh", "curl", "kubectl", "docker", "aws", "psql"} {
		if !slices.Contains(seen, name) {
			t.Errorf("program %q from the design is missing", name)
		}
	}
}

func TestBashCallCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   usage.InsightInput
		want int64
	}{
		{"tool row wins when larger", bashInput(100, program("cat", 30)), 100},
		{"program rows win when larger", bashInput(10, program("cat", 30), program("git", 5)), 35},
		{"negative rows are ignored", bashInput(0, program("cat", -4), program("git", 5)), 5},
		{"empty", usage.InsightInput{}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := bashCallCount(tc.in.Tools); got != tc.want {
				t.Fatalf("bashCallCount = %d, want %d", got, tc.want)
			}
		})
	}
}
