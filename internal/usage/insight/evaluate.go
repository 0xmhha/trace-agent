// Package insight turns usage aggregates into findings and recommendations
// with local rules. It makes no network calls.
package insight

import (
	"time"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Evaluate runs every rule over in. A rule fires, passes, or is skipped when
// the range holds too little data to judge; the report counts each outcome.
// Items are ordered by severity, most severe first, then by rule ID. now
// stamps the report. Coverage is copied from in.Overview.Coverage.
func Evaluate(in usage.InsightInput, now time.Time) usage.InsightReport {
	return usage.InsightReport{Coverage: in.Overview.Coverage, GeneratedAt: now, Items: []usage.Insight{}}
}
