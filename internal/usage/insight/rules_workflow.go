package insight

import (
	"cmp"
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

// compactionText is the message table of the frequent compaction rule.
type compactionText struct {
	title, detail        text
	compacted, mostInOne text
	advice               advice
}

func compactionMessages() compactionText {
	return compactionText{
		// title takes the number of sessions.
		title: text{
			usage.LocaleEnglish: "%d sessions compacted their context repeatedly",
			usage.LocaleKorean:  "맥락 압축이 되풀이된 세션이 %d개입니다",
		},
		// detail takes the compactions per session the rule counts.
		detail: text{
			usage.LocaleEnglish: "These sessions compacted their context %d or more times. Each compaction replaces the details of the earlier conversation with a summary.",
			usage.LocaleKorean:  "이 세션들은 맥락을 %d회 이상 압축했습니다. 압축할 때마다 앞선 대화의 세부 내용이 요약으로 바뀝니다.",
		},
		// compacted takes the compactions per session the rule counts.
		compacted: text{
			usage.LocaleEnglish: "Sessions with %d or more compactions",
			usage.LocaleKorean:  "압축이 %d회 이상인 세션",
		},
		mostInOne: text{usage.LocaleEnglish: "Most compactions in one session", usage.LocaleKorean: "한 세션의 최대 압축 횟수"},
		advice: advice{
			title: text{usage.LocaleEnglish: "Split long tasks", usage.LocaleKorean: "긴 작업 나누기"},
			action: text{
				usage.LocaleEnglish: "Hand exploration and research to sub-agents, and continue in a new session when a step is done.",
				usage.LocaleKorean:  "탐색과 조사는 하위 에이전트에 맡기고, 한 단계가 끝나면 새 세션에서 이어 갑니다.",
			},
		},
	}
}

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
	w, m := newWriter(in.Locale), compactionMessages()
	return verdict{judged: true, findings: []finding{{
		severity: usage.SeverityInfo,
		title:    w.say(m.title, len(compacted)),
		detail:   w.say(m.detail, compactionsPerSession),
		evidence: []usage.Evidence{
			w.countOf(w.say(m.compacted, compactionsPerSession),
				int64(len(compacted)), int64(len(in.Sessions)), unitSessions),
			w.count(w.say(m.mostInOne), compacted[0].Compactions, unitCompactions),
		},
		sessionIDs:     ids,
		recommendation: w.recommend(usage.RecommendHabit, m.advice, ""),
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

// programListSeparator joins program names in a sentence.
const programListSeparator = ", "

// fileReadPrograms are the shell programs a dedicated tool replaces.
func fileReadPrograms() []string {
	return []string{"cat", "grep", "find", "ls", "head", "tail", "sed"}
}

// bashOverToolsText is the message table of the Bash-instead-of-tools rule.
type bashOverToolsText struct {
	title, detail             text
	readCalls, share, program text
	advice                    advice
}

func bashOverToolsMessages() bashOverToolsText {
	return bashOverToolsText{
		// title takes the share of read and search commands in percent.
		title: text{
			usage.LocaleEnglish: "%s%% of Bash calls are file read and search commands",
			usage.LocaleKorean:  "Bash 호출의 %s%%가 파일 읽기와 검색 명령입니다",
		},
		// detail takes the program names and the number of their calls.
		detail: text{
			usage.LocaleEnglish: "Commands such as %s ran %s times through Bash. Dedicated tools read only the parts needed and return results in a consistent format.",
			usage.LocaleKorean:  "%s 같은 명령을 Bash 로 %s회 실행했습니다. 전용 도구를 쓰면 필요한 부분만 읽고 결과도 일정한 형식으로 받습니다.",
		},
		readCalls: text{usage.LocaleEnglish: "File read and search commands", usage.LocaleKorean: "파일 읽기와 검색 명령"},
		share:     text{usage.LocaleEnglish: "Share of Bash calls", usage.LocaleKorean: "Bash 호출 중 비율"},
		// program takes the program name.
		program: text{usage.LocaleEnglish: "%s", usage.LocaleKorean: "%s 실행"},
		advice: advice{
			title: text{usage.LocaleEnglish: "Use Read, Grep and Glob", usage.LocaleKorean: "Read, Grep, Glob 사용"},
			action: text{
				usage.LocaleEnglish: "Note in the project instructions to use Read for file contents, Grep for searching content and Glob for finding files.",
				usage.LocaleKorean:  "파일 내용은 Read, 내용 검색은 Grep, 파일 찾기는 Glob 을 쓰도록 프로젝트 지침에 적어 둡니다.",
			},
		},
	}
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
	w, m := newWriter(in.Locale), bashOverToolsMessages()
	evidence := []usage.Evidence{
		w.countOf(w.say(m.readCalls), readCalls, bashCalls, unitCalls),
		w.share(w.say(m.share), readCalls, bashCalls),
	}
	names := make([]string, 0, bashReadTopPrograms)
	for _, program := range used[:min(len(used), bashReadTopPrograms)] {
		evidence = append(evidence, w.count(w.say(m.program, program.Name), program.Calls, unitRuns))
		names = append(names, program.Name)
	}
	return verdict{judged: true, findings: []finding{{
		severity:       usage.SeverityInfo,
		title:          w.say(m.title, formatShare(readCalls, bashCalls)),
		detail:         w.say(m.detail, strings.Join(names, programListSeparator), formatCount(readCalls)),
		evidence:       evidence,
		recommendation: w.recommend(usage.RecommendHabit, m.advice, ""),
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
	advice   advice
}

// programAdviceTable lists the external programs the rule knows. Adding a
// program or a family means adding it here.
func programAdviceTable() []programAdvice {
	return []programAdvice{
		{
			programs: []string{"gh"},
			kind:     usage.RecommendMCP,
			advice: advice{
				title: text{usage.LocaleEnglish: "Consider an MCP server for GitHub work", usage.LocaleKorean: "GitHub 작업용 MCP 서버 검토"},
				action: text{
					usage.LocaleEnglish: "Check whether repeated work such as looking up issues and pull requests can move to an MCP server for GitHub.",
					usage.LocaleKorean:  "이슈와 풀 리퀘스트 조회처럼 되풀이하는 작업을 GitHub 작업용 MCP 서버로 옮길 수 있는지 검토합니다.",
				},
			},
		},
		{
			programs: []string{"glab"},
			kind:     usage.RecommendMCP,
			advice: advice{
				title: text{usage.LocaleEnglish: "Consider an MCP server for GitLab work", usage.LocaleKorean: "GitLab 작업용 MCP 서버 검토"},
				action: text{
					usage.LocaleEnglish: "Check whether repeated work such as looking up issues and merge requests can move to an MCP server for GitLab.",
					usage.LocaleKorean:  "이슈와 머지 리퀘스트 조회처럼 되풀이하는 작업을 GitLab 작업용 MCP 서버로 옮길 수 있는지 검토합니다.",
				},
			},
		},
		{
			programs: []string{"curl", "wget"},
			kind:     usage.RecommendSkill,
			advice: advice{
				title: text{usage.LocaleEnglish: "Turn repeated HTTP calls into a skill", usage.LocaleKorean: "되풀이하는 HTTP 호출을 스킬로 정리"},
				action: text{
					usage.LocaleEnglish: "If you call the same API again and again, bundle the calling steps into a skill or consider an MCP server for that API.",
					usage.LocaleKorean:  "같은 API 를 되풀이해 부른다면 호출 절차를 스킬로 묶거나 그 API 용 MCP 서버를 검토합니다.",
				},
			},
		},
		{
			programs: []string{"kubectl", "helm"},
			kind:     usage.RecommendMCP,
			advice: advice{
				title: text{usage.LocaleEnglish: "Consider an MCP server for Kubernetes queries", usage.LocaleKorean: "Kubernetes 조회용 MCP 서버 검토"},
				action: text{
					usage.LocaleEnglish: "If you query cluster state often, consider an MCP server for Kubernetes queries or a skill that bundles the query steps.",
					usage.LocaleKorean:  "클러스터 상태를 자주 조회한다면 Kubernetes 조회용 MCP 서버나 조회 절차를 묶은 스킬을 검토합니다.",
				},
			},
		},
		{
			programs: []string{"docker", "podman"},
			kind:     usage.RecommendMCP,
			advice: advice{
				title: text{usage.LocaleEnglish: "Consider an MCP server for container management", usage.LocaleKorean: "컨테이너 관리용 MCP 서버 검토"},
				action: text{
					usage.LocaleEnglish: "If you often list containers and read their logs, consider an MCP server for container management or a skill that bundles those steps.",
					usage.LocaleKorean:  "컨테이너 조회와 로그 확인을 자주 한다면 컨테이너 관리용 MCP 서버나 그 절차를 묶은 스킬을 검토합니다.",
				},
			},
		},
		{
			programs: []string{"aws", "gcloud", "az"},
			kind:     usage.RecommendMCP,
			advice: advice{
				title: text{usage.LocaleEnglish: "Consider an MCP server for cloud resource queries", usage.LocaleKorean: "클라우드 리소스 조회용 MCP 서버 검토"},
				action: text{
					usage.LocaleEnglish: "If you query resource state often, consider an MCP server for cloud resource queries or a skill that bundles the query steps.",
					usage.LocaleKorean:  "리소스 상태를 자주 조회한다면 클라우드 리소스 조회용 MCP 서버나 조회 절차를 묶은 스킬을 검토합니다.",
				},
			},
		},
		{
			programs: []string{"psql", "mysql", "sqlite3", "mongosh", "redis-cli"},
			kind:     usage.RecommendMCP,
			advice: advice{
				title: text{usage.LocaleEnglish: "Consider an MCP server for database queries", usage.LocaleKorean: "데이터베이스 질의용 MCP 서버 검토"},
				action: text{
					usage.LocaleEnglish: "If you query the same database often, consider an MCP server for database queries with read-only access.",
					usage.LocaleKorean:  "같은 데이터베이스에 자주 질의한다면 읽기 전용 권한의 데이터베이스 질의용 MCP 서버를 검토합니다.",
				},
			},
		},
	}
}

// repeatedProgramText is the message table of the repeated program rule. The
// recommendations live in programAdviceTable.
type repeatedProgramText struct {
	title, detail, runs text
}

func repeatedProgramMessages() repeatedProgramText {
	return repeatedProgramText{
		// title takes the program and the number of its runs.
		title: text{
			usage.LocaleEnglish: "The %s command ran %s times",
			usage.LocaleKorean:  "%s 명령을 %s회 실행했습니다",
		},
		// detail takes the number of Bash calls and the program's share of
		// them in percent.
		detail: text{
			usage.LocaleEnglish: "That is %[2]s%% of %[1]s Bash calls. Calling the same external program again and again means building its arguments and parsing its output anew every time.",
			usage.LocaleKorean:  "Bash 호출 %s회 중 %s%%입니다. 같은 외부 프로그램을 되풀이해 부르면 매번 인자를 새로 조립하고 출력을 다시 해석해야 합니다.",
		},
		// runs takes the program.
		runs: text{usage.LocaleEnglish: "%s", usage.LocaleKorean: "%s 실행"},
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
	w, m := newWriter(in.Locale), repeatedProgramMessages()
	v := verdict{judged: true}
	for _, program := range in.Tools.Programs {
		entry, known := advice[program.Name]
		if !known || program.Calls < repeatedProgramMinCalls {
			continue
		}
		v.findings = append(v.findings, finding{
			subject:  program.Name,
			severity: usage.SeverityInfo,
			title:    w.say(m.title, program.Name, formatCount(program.Calls)),
			detail:   w.say(m.detail, formatCount(bashCalls), formatShare(program.Calls, bashCalls)),
			evidence: []usage.Evidence{
				w.countOf(w.say(m.runs, program.Name), program.Calls, bashCalls, unitRuns),
			},
			recommendation: w.recommend(entry.kind, entry.advice, ""),
		})
	}
	return v
}
