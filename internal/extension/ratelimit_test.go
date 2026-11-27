package extension

import (
	"testing"
	"time"
)

// limiterStep moves the clock and then asks for a number of requests.
type limiterStep struct {
	advance time.Duration
	ask     int
	allowed int
}

func TestRateLimiterSlidesItsWindow(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps []limiterStep
	}{
		{"burst is cut at the limit", []limiterStep{
			{0, ratePerWindow + 10, ratePerWindow},
		}},
		{"nothing frees up inside the window", []limiterStep{
			{0, ratePerWindow, ratePerWindow},
			{rateWindow - time.Nanosecond, 1, 0},
			{time.Nanosecond, ratePerWindow + 1, ratePerWindow},
		}},
		{"only the requests older than the window free up", []limiterStep{
			{0, 20, 20},
			{rateWindow / 2, ratePerWindow, ratePerWindow - 20},
			// The first 20 have left the window; the later 30 have not.
			{rateWindow / 2, ratePerWindow, 20},
			{rateWindow / 2, ratePerWindow, ratePerWindow - 20},
		}},
		{"a clock that steps back refuses until it catches up", []limiterStep{
			{0, ratePerWindow, ratePerWindow},
			{-time.Hour, 1, 0},
			{time.Hour + rateWindow, 1, 1},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limiter := &rateLimiter{}
			at := testEpoch()
			for i, step := range tc.steps {
				at = at.Add(step.advance)
				allowed := 0
				for range step.ask {
					if limiter.allow(at) {
						allowed++
					}
				}
				if allowed != step.allowed {
					t.Fatalf("step %d: allowed %d of %d; want %d", i, allowed, step.ask, step.allowed)
				}
			}
		})
	}
}

// A sender that stays exactly at the limit is never refused.
func TestRateLimiterAdmitsSteadyRate(t *testing.T) {
	limiter := &rateLimiter{}
	at := testEpoch()
	for i := range 4 * ratePerWindow {
		if !limiter.allow(at) {
			t.Fatalf("request %d at the steady rate was refused", i)
		}
		at = at.Add(rateWindow / ratePerWindow)
	}
}

// No span of one window may ever hold more than the limit, whatever the
// spacing of the requests.
func TestRateLimiterNeverExceedsTheLimit(t *testing.T) {
	limiter := &rateLimiter{}
	start := testEpoch()
	const (
		step     = 7 * time.Millisecond
		ticks    = 2000
		maxBurst = 4
	)
	var admitted []time.Time
	for i := range ticks {
		at := start.Add(time.Duration(i) * step)
		for range i % maxBurst {
			if limiter.allow(at) {
				admitted = append(admitted, at)
			}
		}
	}
	if len(admitted) <= ratePerWindow {
		t.Fatalf("only %d requests admitted; the test does not exercise the limit", len(admitted))
	}
	for i := ratePerWindow; i < len(admitted); i++ {
		if gap := admitted[i].Sub(admitted[i-ratePerWindow]); gap < rateWindow {
			t.Fatalf("requests %d and %d are %v apart: more than %d in one window", i-ratePerWindow, i, gap, ratePerWindow)
		}
	}
}
