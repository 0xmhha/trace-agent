package insight

import (
	"cmp"
	"slices"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Unused extensions: a range with many sessions never used one kind of
// extension.
//
// Decisions:
//   - The rule fires separately for skills, MCP servers and sub-agents, each
//     only when that kind has no call at all.
//   - A kind counts as used when either the tool rows or the group rows show
//     a call. Skills also count as used when a model call is attributed to
//     one, because a skill started by the user leaves no tool call.
//     Sub-agents also count as used when the overview reports a sub-agent
//     stream.
const unusedExtMinSessions int64 = 20

// Subjects of the unused-extension findings.
const (
	subjectSkill = "skill"
	subjectMCP   = "mcp"
	subjectAgent = "agent"
)

// extensionKind describes one kind of extension the rule checks.
type extensionKind struct {
	subject   string
	used      func(usage.InsightInput) bool
	title     text
	hint      text
	callLabel text
	kind      usage.RecommendationKind
	advice    advice
}

// extensionKinds lists the kinds the rule checks. Adding a kind means adding
// it here.
func extensionKinds() []extensionKind {
	return []extensionKind{
		{
			subject:   subjectSkill,
			used:      skillsUsed,
			title:     text{usage.LocaleEnglish: "No skill was used", usage.LocaleKorean: "스킬을 한 번도 쓰지 않았습니다"},
			hint:      text{usage.LocaleEnglish: "If you repeat a procedure, you can bundle it into a skill and call it in one step.", usage.LocaleKorean: "되풀이하는 절차가 있다면 스킬로 묶어 한 번에 부를 수 있습니다."},
			callLabel: text{usage.LocaleEnglish: "Skill calls", usage.LocaleKorean: "스킬 호출"},
			kind:      usage.RecommendSkill,
			advice: advice{
				title: text{usage.LocaleEnglish: "Turn repeated procedures into skills", usage.LocaleKorean: "되풀이하는 절차를 스킬로 만들기"},
				action: text{
					usage.LocaleEnglish: "Write instructions you give often, such as tidying commits or reviewing code, as skills.",
					usage.LocaleKorean:  "커밋 정리나 코드 리뷰처럼 자주 되풀이하는 지시를 스킬로 정리합니다.",
				},
			},
		},
		{
			subject:   subjectMCP,
			used:      mcpUsed,
			title:     text{usage.LocaleEnglish: "No MCP server was used", usage.LocaleKorean: "MCP 서버를 한 번도 쓰지 않았습니다"},
			hint:      text{usage.LocaleEnglish: "If you often work with an external service, you can connect it directly through an MCP server.", usage.LocaleKorean: "자주 오가는 외부 서비스가 있다면 MCP 서버로 직접 연결할 수 있습니다."},
			callLabel: text{usage.LocaleEnglish: "MCP tool calls", usage.LocaleKorean: "MCP 도구 호출"},
			kind:      usage.RecommendMCP,
			advice: advice{
				title: text{usage.LocaleEnglish: "Connect MCP servers for services you use often", usage.LocaleKorean: "자주 쓰는 외부 서비스에 MCP 서버 연결"},
				action: text{
					usage.LocaleEnglish: "If you often check services such as issue tracking, documents or databases, consider an MCP server for that service.",
					usage.LocaleKorean:  "이슈 관리, 문서, 데이터베이스처럼 자주 확인하는 서비스가 있다면 그 서비스용 MCP 서버를 검토합니다.",
				},
			},
		},
		{
			subject:   subjectAgent,
			used:      agentsUsed,
			title:     text{usage.LocaleEnglish: "No sub-agent was used", usage.LocaleKorean: "하위 에이전트를 한 번도 쓰지 않았습니다"},
			hint:      text{usage.LocaleEnglish: "Handing broad exploration to sub-agents saves the main session's context.", usage.LocaleKorean: "넓은 탐색을 하위 에이전트에 맡기면 주 세션의 맥락을 아낄 수 있습니다."},
			callLabel: text{usage.LocaleEnglish: "Sub-agent calls", usage.LocaleKorean: "하위 에이전트 호출"},
			kind:      usage.RecommendHabit,
			advice: advice{
				title: text{usage.LocaleEnglish: "Hand broad exploration to sub-agents", usage.LocaleKorean: "넓은 탐색을 하위 에이전트에 맡기기"},
				action: text{
					usage.LocaleEnglish: "Ask for research across many files, and for tasks that do not depend on each other, to be handed to sub-agents.",
					usage.LocaleKorean:  "여러 파일을 훑는 조사나 서로 독립된 작업은 하위 에이전트에 맡기도록 지시합니다.",
				},
			},
		},
	}
}

// unusedExtText is the message table the unused-extension findings share.
type unusedExtText struct {
	detail, sessions text
}

func unusedExtMessages() unusedExtText {
	return unusedExtText{
		// detail takes the number of sessions and the kind's hint.
		detail: text{
			usage.LocaleEnglish: "There were no calls across %s sessions. %s",
			usage.LocaleKorean:  "세션 %s개에서 호출이 없었습니다. %s",
		},
		sessions: text{usage.LocaleEnglish: "Sessions in the range", usage.LocaleKorean: "구간의 세션"},
	}
}

func evalUnusedExtensions(in usage.InsightInput) verdict {
	sessions := in.Overview.Sessions
	if sessions < unusedExtMinSessions {
		return verdict{}
	}
	w, m := newWriter(in.Locale), unusedExtMessages()
	v := verdict{judged: true}
	for _, kind := range extensionKinds() {
		if kind.used(in) {
			continue
		}
		v.findings = append(v.findings, finding{
			subject:  kind.subject,
			severity: usage.SeverityInfo,
			title:    w.say(kind.title),
			detail:   w.say(m.detail, formatCount(sessions), w.say(kind.hint)),
			evidence: []usage.Evidence{
				w.count(w.say(kind.callLabel), 0, unitCalls),
				w.count(w.say(m.sessions), sessions, unitSessions),
			},
			recommendation: w.recommend(kind.kind, kind.advice, ""),
		})
	}
	return v
}

func skillsUsed(in usage.InsightInput) bool {
	if categoryCalls(in.Tools.Tools, usage.ToolSkill) > 0 || groupCalls(in.Tools.Skills) > 0 {
		return true
	}
	for _, skill := range in.Tools.Skills {
		if skill.Tokens != nil && totalTokens(*skill.Tokens) > 0 {
			return true
		}
	}
	return false
}

func mcpUsed(in usage.InsightInput) bool {
	return categoryCalls(in.Tools.Tools, usage.ToolMCP) > 0 || groupCalls(in.Tools.MCPServers) > 0
}

func agentsUsed(in usage.InsightInput) bool {
	return categoryCalls(in.Tools.Tools, usage.ToolAgent) > 0 ||
		groupCalls(in.Tools.Subagents) > 0 ||
		in.Overview.Subagents > 0
}

func categoryCalls(tools []usage.ToolUsage, category usage.ToolCategory) int64 {
	var calls int64
	for _, tool := range tools {
		if tool.Category == category {
			calls += max(tool.Calls, 0)
		}
	}
	return calls
}

func groupCalls(groups []usage.GroupUsage) int64 {
	var calls int64
	for _, group := range groups {
		calls += max(group.Calls, 0)
	}
	return calls
}

// Extension event errors: a sender with enough events reports too many
// errors.
//
// Decision: the recommendation kind is "plugin". No kind names a third-party
// sender exactly, and a sender is closest to a plugin: something installed
// next to the client.
const (
	extErrorMinEvents int64 = 20
	extErrorRatePct   int64 = 20
)

// extErrorText is the message table of the extension event error rule.
type extErrorText struct {
	title, detail text
	errors, rate  text
	advice        advice
}

func extErrorMessages() extErrorText {
	return extErrorText{
		// title takes the sender and its error rate in percent.
		title: text{
			usage.LocaleEnglish: "%[2]s%% of events from %[1]s are errors",
			usage.LocaleKorean:  "%s 출처 이벤트의 %s%%가 오류입니다",
		},
		// detail takes the number of events and the number of errors.
		detail: text{
			usage.LocaleEnglish: "%[2]s of %[1]s events arrived with an error status. The tool that sent them may be failing at its work.",
			usage.LocaleKorean:  "이벤트 %s개 중 %s개가 오류 상태로 들어왔습니다. 이벤트를 보낸 도구의 작업이 실패하고 있을 수 있습니다.",
		},
		errors: text{usage.LocaleEnglish: "Error events", usage.LocaleKorean: "오류 이벤트"},
		rate:   text{usage.LocaleEnglish: "Error rate", usage.LocaleKorean: "오류 비율"},
		advice: advice{
			title: text{usage.LocaleEnglish: "Check the tool that sent the events", usage.LocaleKorean: "이벤트를 보낸 도구 점검"},
			action: text{
				usage.LocaleEnglish: "Read the messages of the error events on the extension events page, and check that tool's settings and runtime environment.",
				usage.LocaleKorean:  "확장 이벤트 화면에서 오류 이벤트의 메시지를 확인하고 그 도구의 설정과 실행 환경을 점검합니다.",
			},
		},
	}
}

func evalExtErrorRate(in usage.InsightInput) verdict {
	w, m := newWriter(in.Locale), extErrorMessages()
	var v verdict
	for _, source := range in.Ext {
		if source.Events < extErrorMinEvents {
			continue
		}
		v.judged = true
		if !atLeastPercent(source.Errors, source.Events, extErrorRatePct) {
			continue
		}
		v.findings = append(v.findings, finding{
			subject:  source.Source,
			severity: rateSeverity(source.Errors, source.Events, extErrorRatePct),
			title:    w.say(m.title, w.name(source.Source), formatShare(source.Errors, source.Events)),
			detail:   w.say(m.detail, formatCount(source.Events), formatCount(source.Errors)),
			evidence: []usage.Evidence{
				w.countOf(w.say(m.errors), source.Errors, source.Events, unitEvents),
				w.share(w.say(m.rate), source.Errors, source.Events),
			},
			recommendation: w.recommend(usage.RecommendPlugin, m.advice, source.Source),
		})
	}
	return v
}

// Rejected extension input: the endpoint refused at least one input.
//
// Decisions:
//   - Refusals are not timestamped, so the count is cumulative and does not
//     follow the range. The detail says so.
//   - When no sender has events in the range and nothing was refused, the
//     endpoint is unused and the rule is skipped.
//   - The severity is warn: a refused input is data the user expected to see
//     and did not.
const (
	extRejectedMin int64 = 1
	// extRejectedTopSources is how many senders the evidence names.
	extRejectedTopSources = 3
)

// extRejectedText is the message table of the rejected extension input rule.
type extRejectedText struct {
	title, detail                    text
	rejected, bySender, unattributed text
	advice                           advice
}

func extRejectedMessages() extRejectedText {
	return extRejectedText{
		// title takes the number of refused inputs.
		title: text{
			usage.LocaleEnglish: "%s extension inputs were rejected",
			usage.LocaleKorean:  "확장 입력 %s건이 거부됐습니다",
		},
		detail: text{
			usage.LocaleEnglish: "Events in the wrong format are not stored. The rejection count is cumulative and does not follow the selected range.",
			usage.LocaleKorean:  "형식이 맞지 않는 이벤트는 저장하지 않습니다. 거부 횟수는 선택한 구간과 상관없이 누적된 값입니다.",
		},
		rejected: text{usage.LocaleEnglish: "Rejected inputs", usage.LocaleKorean: "거부된 입력"},
		// bySender takes the sender.
		bySender:     text{usage.LocaleEnglish: "Rejected from %s", usage.LocaleKorean: "%s 출처의 거부"},
		unattributed: text{usage.LocaleEnglish: "Rejected from an unknown sender", usage.LocaleKorean: "출처를 알 수 없는 거부"},
		advice: advice{
			title: text{usage.LocaleEnglish: "Check the sending format", usage.LocaleKorean: "전송 형식 점검"},
			action: text{
				usage.LocaleEnglish: "Compare the field names and value formats of the sending tool with the extension event API documentation.",
				usage.LocaleKorean:  "이벤트를 보내는 도구의 필드 이름과 값 형식을 확장 이벤트 API 문서와 맞춰 봅니다.",
			},
		},
	}
}

func evalExtRejected(in usage.InsightInput) verdict {
	rejected := in.ExtRejected
	if rejected < extRejectedMin {
		return verdict{judged: len(in.Ext) > 0}
	}
	w, m := newWriter(in.Locale), extRejectedMessages()
	evidence := []usage.Evidence{w.count(w.say(m.rejected), rejected, unitInputs)}
	senders := make([]usage.ExtSourceRow, 0, len(in.Ext))
	var attributed int64
	for _, source := range in.Ext {
		if source.Rejected > 0 {
			senders = append(senders, source)
			attributed += source.Rejected
		}
	}
	slices.SortStableFunc(senders, func(a, b usage.ExtSourceRow) int {
		return cmp.Or(cmp.Compare(b.Rejected, a.Rejected), cmp.Compare(a.Source, b.Source))
	})
	for _, source := range senders[:min(len(senders), extRejectedTopSources)] {
		evidence = append(evidence, w.count(w.say(m.bySender, w.name(source.Source)), source.Rejected, unitInputs))
	}
	if unattributed := rejected - attributed; unattributed > 0 {
		evidence = append(evidence, w.count(w.say(m.unattributed), unattributed, unitInputs))
	}
	return verdict{judged: true, findings: []finding{{
		severity:       usage.SeverityWarn,
		title:          w.say(m.title, formatCount(rejected)),
		detail:         w.say(m.detail),
		evidence:       evidence,
		recommendation: w.recommend(usage.RecommendPlugin, m.advice, ""),
	}}}
}
