package insight

import (
	"math"
	"testing"

	"github.com/0xmhha/trace-agent/internal/usage"
)

func TestAtLeastPercent(t *testing.T) {
	for _, tc := range []struct {
		name             string
		part, whole, pct int64
		want             bool
	}{
		{"exactly on the threshold", 3, 20, 15, true},
		{"just below the threshold", 149, 1000, 15, false},
		{"above the threshold", 16, 100, 15, true},
		{"zero whole never reaches", 5, 0, 15, false},
		{"negative whole never reaches", 5, -10, 15, false},
		{"large token counts do not lose precision", 2_500_000_000_001, 10_000_000_000_000, 25, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := atLeastPercent(tc.part, tc.whole, tc.pct); got != tc.want {
				t.Fatalf("atLeastPercent(%d, %d, %d) = %v, want %v", tc.part, tc.whole, tc.pct, got, tc.want)
			}
		})
	}
}

func TestPercentOf(t *testing.T) {
	for _, tc := range []struct {
		name        string
		part, whole int64
		want        float64
	}{
		{"whole number", 126, 900, 14},
		{"one decimal place", 7, 30, 23.3},
		{"rounds half up", 1, 16, 6.3},
		{"zero whole", 4, 0, 0},
		{"all", 9, 9, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := percentOf(tc.part, tc.whole); math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("percentOf(%d, %d) = %v, want %v", tc.part, tc.whole, got, tc.want)
			}
		})
	}
}

func TestRateSeverity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		part, whole int64
		want        usage.Severity
	}{
		{"at the threshold", 15, 100, usage.SeverityWarn},
		{"just below double", 299, 1000, usage.SeverityWarn},
		{"at double", 30, 100, usage.SeverityCritical},
		{"above double", 90, 100, usage.SeverityCritical},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := rateSeverity(tc.part, tc.whole, 15); got != tc.want {
				t.Fatalf("rateSeverity(%d, %d, 15) = %q, want %q", tc.part, tc.whole, got, tc.want)
			}
		})
	}
}

func TestFormatting(t *testing.T) {
	for _, tc := range []struct {
		name, got, want string
	}{
		{"count below a thousand", formatCount(999), "999"},
		{"count with one separator", formatCount(1000), "1,000"},
		{"count with two separators", formatCount(1234567), "1,234,567"},
		{"zero count", formatCount(0), "0"},
		{"negative count", formatCount(-12345), "-12,345"},
		{"whole percent", formatPercent(14), "14"},
		{"percent with a decimal", formatPercent(23.3), "23.3"},
		{"share", formatShare(126, 900), "14"},
		{"seconds", formatSeconds(1240), "1.2초"},
		{"whole seconds keep a decimal", formatSeconds(2000), "2.0초"},
		{"named row", displayName("Bash"), "Bash"},
		{"unnamed row", displayName(""), unnamedLabel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

func TestEvidenceBuilders(t *testing.T) {
	plain := count("label", 3, unitTimes)
	if plain.Value != 3 || plain.Unit != unitTimes || plain.Of != nil {
		t.Errorf("count = %+v", plain)
	}
	outOf := countOf("label", 3, 20, unitTimes)
	if outOf.Value != 3 || outOf.Of == nil || *outOf.Of != 20 {
		t.Errorf("countOf = %+v", outOf)
	}
	if got := share("label", 3, 20); got.Value != 15 || got.Unit != unitPercent || got.Of != nil {
		t.Errorf("share = %+v", got)
	}
	if got := millis("label", 1240.4); got.Value != 1240 || got.Unit != unitMillis {
		t.Errorf("millis = %+v", got)
	}
	if got := totalTokens(usage.TokenTotals{Input: 1, Output: 2, CacheRead: 3, CacheCreate: 4}); got != 10 {
		t.Errorf("totalTokens = %d, want 10", got)
	}
}
