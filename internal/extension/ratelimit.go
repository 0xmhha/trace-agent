package extension

import (
	"sync"
	"time"
)

// Request rate the endpoint admits: at most ratePerWindow requests in any
// span of rateWindow.
const (
	ratePerWindow = 50
	rateWindow    = time.Second
)

// rateLimiter is a sliding-window limiter over the caller's clock readings.
// It keeps the times of the last ratePerWindow admitted requests and needs no
// background goroutine. Readings are expected not to run backwards; if the
// clock steps back, requests are refused until it catches up with the oldest
// kept reading, which fails closed and heals by itself.
type rateLimiter struct {
	mu     sync.Mutex
	stamps [ratePerWindow]time.Time
	used   int
	oldest int
}

// allow reports whether a request received at the given time is within the
// rate, and records it when it is.
func (l *rateLimiter) allow(at time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used < len(l.stamps) {
		l.stamps[l.used] = at
		l.used++
		return true
	}
	if at.Sub(l.stamps[l.oldest]) < rateWindow {
		return false
	}
	l.stamps[l.oldest] = at
	l.oldest = (l.oldest + 1) % len(l.stamps)
	return true
}
