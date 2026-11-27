package insight

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Frequent compaction: several sessions compacted their context repeatedly.
//
// Decisions:
//   - The rule reads the session list, which holds the heaviest sessions of
//     the range. Sessions beyond that list are not seen; a session that
//     compacts twice is heavy, so few are missed.
//   - With fewer sessions than the rule needs to fire, it is skipped.
//   - SessionIDs lists the sessions with the most compactions first.
const (
	compactionsPerSession int64 = 2
	compactionMinSessions int   = 3
)

func evalFrequentCompaction(in usage.InsightInput) verdict {
	if len(in.Sessions) < compactionMinSessions {
		return verdict{}
	}
	var compacted []usage.SessionRow
	for _, session := range in.Sessions {
		if session.Compactions >= compactionsPerSession {
			compacted = append(compacted, session)
		}
	}
	if len(compacted) < compactionMinSessions {
		return verdict{judged: true}
	}
	slices.SortStableFunc(compacted, func(a, b usage.SessionRow) int {
		return cmp.Or(cmp.Compare(b.Compactions, a.Compactions), cmp.Compare(a.ID, b.ID))
	})
	ids := make([]string, 0, len(compacted))
	for _, session := range compacted {
		ids = append(ids, session.ID)
	}
	return verdict{judged: true, findings: []finding{{
		severity: usage.SeverityInfo,
		title:    fmt.Sprintf("맥락 압축이 되풀이된 세션이 %d개입니다", len(compacted)),
		detail: fmt.Sprintf("이 세션들은 맥락을 %d회 이상 압축했습니다. 압축할 때마다 앞선 대화의 세부 내용이 요약으로 바뀝니다.",
			compactionsPerSession),
		evidence: []usage.Evidence{
			countOf(fmt.Sprintf("압축이 %d회 이상인 세션", compactionsPerSession),
				int64(len(compacted)), int64(len(in.Sessions)), unitItems),
			count("한 세션의 최대 압축 횟수", compacted[0].Compactions, unitTimes),
		},
		sessionIDs: ids,
		recommendation: &usage.Recommendation{
			Kind:   usage.RecommendHabit,
			Title:  "긴 작업 나누기",
			Action: "탐색과 조사는 하위 에이전트에 맡기고, 한 단계가 끝나면 새 세션에서 이어 갑니다.",
		},
	}}}
}

// Bash instead of dedicated tools: a large share of Bash calls only reads or
// searches files.
//
// Decisions:
//   - Bash calls are the calls of the Bash tool row. When that row is missing
//     or smaller than the program rows, the program rows are the total, so
//     the share never exceeds 100%.
//   - With fewer Bash calls than the count threshold the rule cannot fire and
//     is skipped.
const (
	bashReadMinCalls int64 = 50
	bashReadSharePct int64 = 30
	// bashReadTopPrograms is how many programs the evidence names.
	bashReadTopPrograms = 3
)

// fileReadPrograms are the shell programs a dedicated tool replaces.
func fileReadPrograms() []string {
	return []string{"cat", "grep", "find", "ls", "head", "tail", "sed"}
}

func evalBashOverTools(in usage.InsightInput) verdict {
	bashCalls := bashCallCount(in.Tools)
	if bashCalls < bashReadMinCalls {
		return verdict{}
	}
	replaceable := fileReadPrograms()
	var (
		readCalls int64
		used      []usage.GroupUsage
	)
	for _, program := range in.Tools.Programs {
		if program.Calls > 0 && slices.Contains(replaceable, program.Name) {
			readCalls += program.Calls
			used = append(used, program)
		}
	}
	if readCalls < bashReadMinCalls || !atLeastPercent(readCalls, bashCalls, bashReadSharePct) {
		return verdict{judged: true}
	}
	sortByCalls(used)
	evidence := []usage.Evidence{
		countOf("파일 읽기와 검색 명령", readCalls, bashCalls, unitTimes),
		share("Bash 호출 중 비율", readCalls, bashCalls),
	}
	names := make([]string, 0, bashReadTopPrograms)
	for _, program := range used[:min(len(used), bashReadTopPrograms)] {
		evidence = append(evidence, count(program.Name+" 실행", program.Calls, unitTimes))
		names = append(names, program.Name)
	}
	return verdict{judged: true, findings: []finding{{
		severity: usage.SeverityInfo,
		title:    fmt.Sprintf("Bash 호출의 %s%%가 파일 읽기와 검색 명령입니다", formatShare(readCalls, bashCalls)),
		detail: fmt.Sprintf("%s 같은 명령을 Bash 로 %s회 실행했습니다. 전용 도구를 쓰면 필요한 부분만 읽고 결과도 일정한 형식으로 받습니다.",
			strings.Join(names, ", "), formatCount(readCalls)),
		evidence: evidence,
		recommendation: &usage.Recommendation{
			Kind:   usage.RecommendHabit,
			Title:  "Read, Grep, Glob 사용",
			Action: "파일 내용은 Read, 내용 검색은 Grep, 파일 찾기는 Glob 을 쓰도록 프로젝트 지침에 적어 둡니다.",
		},
	}}}
}

// bashCallCount is the number of Bash calls of the range.
func bashCallCount(tools usage.Tools) int64 {
	var toolCalls, programCalls int64
	for _, tool := range tools.Tools {
		if tool.Name == usage.ToolNameBash {
			toolCalls += tool.Calls
		}
	}
	for _, program := range tools.Programs {
		programCalls += max(program.Calls, 0)
	}
	return max(toolCalls, programCalls)
}

// sortByCalls orders groups by calls, most first, then by name.
func sortByCalls(groups []usage.GroupUsage) {
	slices.SortStableFunc(groups, func(a, b usage.GroupUsage) int {
		return cmp.Or(cmp.Compare(b.Calls, a.Calls), cmp.Compare(a.Name, b.Name))
	})
}

// Repeated external program: one external program is run through Bash so
// often that a dedicated integration is worth a look.
//
// Decisions:
//   - Only programs in the advice table are judged; the table maps each to a
//     category of integration, never to a named product.
//   - With fewer Bash calls than the threshold no program can reach it and
//     the rule is skipped.
const repeatedProgramMinCalls int64 = 50

// programAdvice is the recommendation shared by a family of programs.
type programAdvice struct {
	programs []string
	kind     usage.RecommendationKind
	title    string
	action   string
}

// programAdviceTable lists the external programs the rule knows. Adding a
// program or a family means adding it here.
func programAdviceTable() []programAdvice {
	return []programAdvice{
		{
			programs: []string{"gh"},
			kind:     usage.RecommendMCP,
			title:    "GitHub 작업용 MCP 서버 검토",
			action:   "이슈와 풀 리퀘스트 조회처럼 되풀이하는 작업을 GitHub 작업용 MCP 서버로 옮길 수 있는지 검토합니다.",
		},
		{
			programs: []string{"glab"},
			kind:     usage.RecommendMCP,
			title:    "GitLab 작업용 MCP 서버 검토",
			action:   "이슈와 머지 리퀘스트 조회처럼 되풀이하는 작업을 GitLab 작업용 MCP 서버로 옮길 수 있는지 검토합니다.",
		},
		{
			programs: []string{"curl", "wget"},
			kind:     usage.RecommendSkill,
			title:    "되풀이하는 HTTP 호출을 스킬로 정리",
			action:   "같은 API 를 되풀이해 부른다면 호출 절차를 스킬로 묶거나 그 API 용 MCP 서버를 검토합니다.",
		},
		{
			programs: []string{"kubectl", "helm"},
			kind:     usage.RecommendMCP,
			title:    "Kubernetes 조회용 MCP 서버 검토",
			action:   "클러스터 상태를 자주 조회한다면 Kubernetes 조회용 MCP 서버나 조회 절차를 묶은 스킬을 검토합니다.",
		},
		{
			programs: []string{"docker", "podman"},
			kind:     usage.RecommendMCP,
			title:    "컨테이너 관리용 MCP 서버 검토",
			action:   "컨테이너 조회와 로그 확인을 자주 한다면 컨테이너 관리용 MCP 서버나 그 절차를 묶은 스킬을 검토합니다.",
		},
		{
			programs: []string{"aws", "gcloud", "az"},
			kind:     usage.RecommendMCP,
			title:    "클라우드 리소스 조회용 MCP 서버 검토",
			action:   "리소스 상태를 자주 조회한다면 클라우드 리소스 조회용 MCP 서버나 조회 절차를 묶은 스킬을 검토합니다.",
		},
		{
			programs: []string{"psql", "mysql", "sqlite3", "mongosh", "redis-cli"},
			kind:     usage.RecommendMCP,
			title:    "데이터베이스 질의용 MCP 서버 검토",
			action:   "같은 데이터베이스에 자주 질의한다면 읽기 전용 권한의 데이터베이스 질의용 MCP 서버를 검토합니다.",
		},
	}
}

func evalRepeatedProgram(in usage.InsightInput) verdict {
	bashCalls := bashCallCount(in.Tools)
	if bashCalls < repeatedProgramMinCalls {
		return verdict{}
	}
	advice := make(map[string]programAdvice)
	for _, entry := range programAdviceTable() {
		for _, program := range entry.programs {
			advice[program] = entry
		}
	}
	v := verdict{judged: true}
	for _, program := range in.Tools.Programs {
		entry, known := advice[program.Name]
		if !known || program.Calls < repeatedProgramMinCalls {
			continue
		}
		v.findings = append(v.findings, finding{
			subject:  program.Name,
			severity: usage.SeverityInfo,
			title:    fmt.Sprintf("%s 명령을 %s회 실행했습니다", program.Name, formatCount(program.Calls)),
			detail: fmt.Sprintf("Bash 호출 %s회 중 %s%%입니다. 같은 외부 프로그램을 되풀이해 부르면 매번 인자를 새로 조립하고 출력을 다시 해석해야 합니다.",
				formatCount(bashCalls), formatShare(program.Calls, bashCalls)),
			evidence: []usage.Evidence{
				countOf(program.Name+" 실행", program.Calls, bashCalls, unitTimes),
			},
			recommendation: &usage.Recommendation{Kind: entry.kind, Title: entry.title, Action: entry.action},
		})
	}
	return v
}
