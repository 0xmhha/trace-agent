// Package insight turns usage aggregates into findings and recommendations
// with local rules. It makes no network calls.
package insight

import (
	"cmp"
	"slices"
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// ruleID is the stable kebab-case identifier of a rule. It is part of the
// JSON contract through Insight.RuleID, so an ID is never renamed.
type ruleID string

// Rule identifiers, in the order of the rule table in the design.
const (
	ruleToolErrorRate      ruleID = "tool-error-rate"
	ruleMCPServerErrorRate ruleID = "mcp-server-error-rate"
	ruleSlowHook           ruleID = "slow-hook"
	ruleFailingHook        ruleID = "failing-hook"
	ruleLowCacheHit        ruleID = "low-cache-hit"
	ruleFrequentCompaction ruleID = "frequent-compaction"
	ruleBashOverTools      ruleID = "bash-instead-of-tools"
	ruleRepeatedProgram    ruleID = "repeated-external-program"
	ruleUnusedExtensions   ruleID = "unused-extensions"
	ruleTokenConcentration ruleID = "token-concentration"
	ruleSingleModel        ruleID = "single-model"
	ruleExtErrorRate       ruleID = "ext-error-rate"
	ruleExtRejected        ruleID = "ext-rejected"
)

// subjectSeparator joins a rule ID and a subject into an insight ID.
const subjectSeparator = ":"

// maxSessionIDs caps the sessions one insight points at, so that the page
// links to a short list instead of every matching session.
const maxSessionIDs = 5

// rule is one registered check. eval is a pure function of its input.
type rule struct {
	id       ruleID
	category usage.InsightCategory
	eval     func(usage.InsightInput) verdict
}

// rules is the single registry: adding a rule means adding one line here.
// It returns a fresh slice so that no caller can alter the set for others.
func rules() []rule {
	return []rule{
		{ruleToolErrorRate, usage.CategoryReliability, evalToolErrorRate},
		{ruleMCPServerErrorRate, usage.CategoryReliability, evalMCPServerErrorRate},
		{ruleSlowHook, usage.CategoryWorkflow, evalSlowHook},
		{ruleFailingHook, usage.CategoryReliability, evalFailingHook},
		{ruleLowCacheHit, usage.CategoryCost, evalLowCacheHit},
		{ruleFrequentCompaction, usage.CategoryWorkflow, evalFrequentCompaction},
		{ruleBashOverTools, usage.CategoryWorkflow, evalBashOverTools},
		{ruleRepeatedProgram, usage.CategoryWorkflow, evalRepeatedProgram},
		{ruleUnusedExtensions, usage.CategoryExtension, evalUnusedExtensions},
		{ruleTokenConcentration, usage.CategoryCost, evalTokenConcentration},
		{ruleSingleModel, usage.CategoryCost, evalSingleModel},
		{ruleExtErrorRate, usage.CategoryExtension, evalExtErrorRate},
		{ruleExtRejected, usage.CategoryExtension, evalExtRejected},
	}
}

// verdict is what a rule returns. judged is false when the range holds too
// little data for the rule to say anything; findings is then ignored. A judged
// verdict without findings means the rule passed.
type verdict struct {
	judged   bool
	findings []finding
}

// finding is one insight before the registry stamps the rule's identity on
// it. subject distinguishes the findings of a rule that can fire several
// times and is empty for a rule that fires at most once.
type finding struct {
	subject        string
	severity       usage.Severity
	title          string
	detail         string
	evidence       []usage.Evidence
	sessionIDs     []string
	recommendation *usage.Recommendation
}

// outcome is how one rule ended for one input.
type outcome int

// Rule outcomes.
const (
	outcomeSkipped outcome = iota + 1
	outcomePassed
	outcomeFired
)

// run evaluates the rule and turns its findings into insights. The rule's ID
// and category are stamped here so that a rule cannot mislabel its output.
func (r rule) run(in usage.InsightInput) (outcome, []usage.Insight) {
	v := r.eval(in)
	if !v.judged {
		return outcomeSkipped, nil
	}
	if len(v.findings) == 0 {
		return outcomePassed, nil
	}
	items := make([]usage.Insight, 0, len(v.findings))
	for _, f := range v.findings {
		items = append(items, usage.Insight{
			ID:             insightID(r.id, f.subject),
			RuleID:         string(r.id),
			Severity:       f.severity,
			Category:       r.category,
			Title:          f.title,
			Detail:         f.detail,
			Evidence:       nonNilEvidence(f.evidence),
			SessionIDs:     capSessionIDs(f.sessionIDs),
			Recommendation: f.recommendation,
		})
	}
	return outcomeFired, items
}

// insightID is the rule ID alone, or the rule ID and the subject when the
// rule fires per subject.
func insightID(id ruleID, subject string) string {
	if subject == "" {
		return string(id)
	}
	return string(id) + subjectSeparator + subject
}

func nonNilEvidence(evidence []usage.Evidence) []usage.Evidence {
	if evidence == nil {
		return []usage.Evidence{}
	}
	return evidence
}

func capSessionIDs(ids []string) []string {
	if len(ids) > maxSessionIDs {
		return ids[:maxSessionIDs]
	}
	return ids
}

// Evaluate runs every rule over in. A rule fires, passes, or is skipped when
// the range holds too little data to judge; the report counts each outcome.
// Items are ordered by severity, most severe first, then by rule ID. now
// stamps the report. Coverage is copied from in.Overview.Coverage.
func Evaluate(in usage.InsightInput, now time.Time) usage.InsightReport {
	report := usage.InsightReport{
		Coverage:    in.Overview.Coverage,
		GeneratedAt: now,
		Items:       []usage.Insight{},
	}
	for _, r := range rules() {
		result, items := r.run(in)
		switch result {
		case outcomeFired:
			report.RulesFired++
			report.Items = append(report.Items, items...)
		case outcomePassed:
			report.RulesPassed++
		case outcomeSkipped:
			report.RulesSkipped++
		}
	}
	slices.SortStableFunc(report.Items, compareInsights)
	return report
}

// compareInsights orders by severity, then rule ID, then insight ID. The sort
// is stable, so two findings with the same ID keep the order of the input.
func compareInsights(a, b usage.Insight) int {
	return cmp.Or(
		cmp.Compare(severityRank(a.Severity), severityRank(b.Severity)),
		cmp.Compare(a.RuleID, b.RuleID),
		cmp.Compare(a.ID, b.ID),
	)
}

// Severity ranks; a lower rank sorts first.
const (
	rankCritical = iota
	rankWarn
	rankInfo
	rankUnknown
)

func severityRank(s usage.Severity) int {
	switch s {
	case usage.SeverityCritical:
		return rankCritical
	case usage.SeverityWarn:
		return rankWarn
	case usage.SeverityInfo:
		return rankInfo
	default:
		return rankUnknown
	}
}
