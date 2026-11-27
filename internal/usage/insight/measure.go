package insight

import (
	"math"
	"strconv"
	"strings"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// Evidence units shown next to a number.
const (
	unitTimes   = "회"
	unitPercent = "%"
	unitMillis  = "ms"
	unitItems   = "개"
	unitCases   = "건"
)

// unnamedLabel stands in for a row whose name the transcript left empty.
const unnamedLabel = "(이름 없음)"

const (
	// percentScale converts a share to percent.
	percentScale = 100
	// percentDecimalScale keeps one decimal place of a percentage.
	percentDecimalScale = 10
	// criticalFactor is how many times its threshold a rate or duration must
	// reach for a warning to become critical.
	criticalFactor = 2
	// millisPerSecond converts hook durations for display.
	millisPerSecond = 1000
	// groupSize is the number of digits between thousands separators.
	groupSize = 3
)

// atLeastPercent reports whether part out of whole is pct percent or more.
// It compares integers, so a rate that sits exactly on a threshold is never
// lost to floating-point rounding.
func atLeastPercent(part, whole, pct int64) bool {
	return whole > 0 && part*percentScale >= whole*pct
}

// percentOf is part out of whole in percent, rounded to one decimal place.
func percentOf(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return roundPercent(float64(part) / float64(whole) * percentScale)
}

func roundPercent(p float64) float64 {
	return math.Round(p*percentDecimalScale) / percentDecimalScale
}

// rateSeverity is warn for a rate at its threshold and critical once the rate
// reaches criticalFactor times the threshold.
func rateSeverity(part, whole, thresholdPct int64) usage.Severity {
	if atLeastPercent(part, whole, thresholdPct*criticalFactor) {
		return usage.SeverityCritical
	}
	return usage.SeverityWarn
}

// count is an evidence row holding a plain number.
func count(label string, value int64, unit string) usage.Evidence {
	return usage.Evidence{Label: label, Value: float64(value), Unit: unit}
}

// countOf is an evidence row holding "value out of total".
func countOf(label string, value, total int64, unit string) usage.Evidence {
	of := float64(total)
	return usage.Evidence{Label: label, Value: float64(value), Unit: unit, Of: &of}
}

// share is an evidence row holding part out of whole as a percentage.
func share(label string, part, whole int64) usage.Evidence {
	return usage.Evidence{Label: label, Value: percentOf(part, whole), Unit: unitPercent}
}

// millis is an evidence row holding a duration in milliseconds.
func millis(label string, ms float64) usage.Evidence {
	return usage.Evidence{Label: label, Value: math.Round(ms), Unit: unitMillis}
}

// formatCount writes n with thousands separators, as the page shows counts.
func formatCount(n int64) string {
	digits := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var b strings.Builder
	b.WriteString(sign)
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%groupSize == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return b.String()
}

// formatPercent writes a percentage that is already rounded to one decimal
// place, without a trailing ".0".
func formatPercent(p float64) string {
	return strconv.FormatFloat(p, 'f', -1, 64)
}

// formatShare writes part out of whole as a percentage.
func formatShare(part, whole int64) string {
	return formatPercent(percentOf(part, whole))
}

// formatSeconds writes a duration given in milliseconds as seconds with one
// decimal place.
func formatSeconds(ms float64) string {
	return strconv.FormatFloat(ms/millisPerSecond, 'f', 1, 64) + "초"
}

// displayName is name, or a placeholder when the name is empty.
func displayName(name string) string {
	if name == "" {
		return unnamedLabel
	}
	return name
}

// totalTokens adds the four token kinds the model API reports.
func totalTokens(t usage.TokenTotals) int64 {
	return t.Input + t.Output + t.CacheRead + t.CacheCreate
}
