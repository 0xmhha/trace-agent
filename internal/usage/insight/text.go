package insight

import (
	"fmt"
	"math"
	"strconv"

	"github.com/0xmhha/trace-agent/internal/usage"
)

// text is one user-facing message with an entry per supported locale. The
// message tables of the rules hold these, so adding a locale means adding one
// entry to every text. A message with arguments is a fmt format; both entries
// take the same arguments and use explicit indexes where the word order
// differs.
type text map[usage.Locale]string

// advice is the localized title and action of a recommendation.
type advice struct {
	title  text
	action text
}

// unit names the unit of an evidence value. The unit table words it per
// locale: English needs a word that reads well after the number, while
// Korean uses a counter that several units share.
type unit string

// Evidence units.
const (
	unitCalls       unit = "calls"
	unitRuns        unit = "runs"
	unitCompactions unit = "compactions"
	unitSessions    unit = "sessions"
	unitTokens      unit = "tokens"
	unitModels      unit = "models"
	unitEvents      unit = "events"
	unitInputs      unit = "inputs"
	unitPercent     unit = "percent"
	unitMillis      unit = "ms"
)

// Korean counters shared by several units.
const (
	koCounterTimes = "회"
	koCounterItems = "개"
	koCounterCases = "건"
)

// Unit words shared by every locale.
const (
	symbolPercent = "%"
	symbolMillis  = "ms"
)

// unitTable words every unit. It returns a fresh map so that no caller can
// alter it for others.
func unitTable() map[unit]text {
	return map[unit]text{
		unitCalls:       {usage.LocaleEnglish: "calls", usage.LocaleKorean: koCounterTimes},
		unitRuns:        {usage.LocaleEnglish: "runs", usage.LocaleKorean: koCounterTimes},
		unitCompactions: {usage.LocaleEnglish: "compactions", usage.LocaleKorean: koCounterTimes},
		unitSessions:    {usage.LocaleEnglish: "sessions", usage.LocaleKorean: koCounterItems},
		unitTokens:      {usage.LocaleEnglish: "tokens", usage.LocaleKorean: koCounterItems},
		unitModels:      {usage.LocaleEnglish: "models", usage.LocaleKorean: koCounterItems},
		unitEvents:      {usage.LocaleEnglish: "events", usage.LocaleKorean: koCounterItems},
		unitInputs:      {usage.LocaleEnglish: "inputs", usage.LocaleKorean: koCounterCases},
		unitPercent:     {usage.LocaleEnglish: symbolPercent, usage.LocaleKorean: symbolPercent},
		unitMillis:      {usage.LocaleEnglish: symbolMillis, usage.LocaleKorean: symbolMillis},
	}
}

// commonText holds the messages that several rules share.
type commonText struct {
	unnamed text
	seconds text
}

func commonMessages() commonText {
	return commonText{
		// unnamed stands in for a row whose name the transcript left empty.
		unnamed: text{usage.LocaleEnglish: "(unnamed)", usage.LocaleKorean: "(이름 없음)"},
		// seconds takes the formatted number of seconds.
		seconds: text{usage.LocaleEnglish: "%s s", usage.LocaleKorean: "%s초"},
	}
}

// writer words the text of one evaluation in one locale.
type writer struct {
	locale usage.Locale
}

// newWriter writes in l, or in the default locale when l is not supported.
func newWriter(l usage.Locale) writer {
	return writer{locale: usage.NormalizeLocale(l)}
}

// say formats t in the writer's locale. An entry missing for the locale falls
// back to the default locale, so the page shows English rather than nothing.
func (w writer) say(t text, args ...any) string {
	format, ok := t[w.locale]
	if !ok || format == "" {
		format = t[usage.LocaleDefault]
	}
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// recommend builds a recommendation from localized advice.
func (w writer) recommend(kind usage.RecommendationKind, a advice, target string) *usage.Recommendation {
	return &usage.Recommendation{Kind: kind, Title: w.say(a.title), Action: w.say(a.action), Target: target}
}

// unit words u in the writer's locale.
func (w writer) unit(u unit) string {
	return w.say(unitTable()[u])
}

// count is an evidence row holding a plain number.
func (w writer) count(label string, value int64, u unit) usage.Evidence {
	return usage.Evidence{Label: label, Value: float64(value), Unit: w.unit(u)}
}

// countOf is an evidence row holding "value out of total".
func (w writer) countOf(label string, value, total int64, u unit) usage.Evidence {
	of := float64(total)
	return usage.Evidence{Label: label, Value: float64(value), Unit: w.unit(u), Of: &of}
}

// percent is an evidence row holding a percentage that is already rounded.
func (w writer) percent(label string, pct float64) usage.Evidence {
	return usage.Evidence{Label: label, Value: pct, Unit: w.unit(unitPercent)}
}

// share is an evidence row holding part out of whole as a percentage.
func (w writer) share(label string, part, whole int64) usage.Evidence {
	return w.percent(label, percentOf(part, whole))
}

// millis is an evidence row holding a duration in milliseconds.
func (w writer) millis(label string, ms float64) usage.Evidence {
	return usage.Evidence{Label: label, Value: math.Round(ms), Unit: w.unit(unitMillis)}
}

// seconds writes a duration given in milliseconds as seconds with one decimal
// place.
func (w writer) seconds(ms float64) string {
	return w.say(commonMessages().seconds, strconv.FormatFloat(ms/millisPerSecond, 'f', 1, 64))
}

// name is name, or a placeholder when the name is empty.
func (w writer) name(name string) string {
	if name == "" {
		return w.say(commonMessages().unnamed)
	}
	return name
}
