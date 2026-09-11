package web

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// limiter is a per-address token bucket, kept in memory.
//
// In memory is the right amount of machinery here: one process, one event, and
// a limit whose job is to stop a script filling the shrine with a thousand
// invented names -- not to survive a restart. Losing the counters when the
// binary is replaced is a feature on the day somebody is being a nuisance and
// we redeploy.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter() *limiter {
	l := &limiter{buckets: map[string]*bucket{}}

	// Sweep, so a long run does not accumulate one entry per address that has
	// ever visited. An hour is far longer than any bucket takes to refill, so
	// dropping an idle one loses nothing.
	go func() {
		for range time.Tick(time.Hour) {
			l.mu.Lock()
			for k, b := range l.buckets {
				if time.Since(b.last) > time.Hour {
					delete(l.buckets, k)
				}
			}
			l.mu.Unlock()
		}
	}()

	return l
}

// allow takes a token from key's bucket, refilling at rate per second up to
// burst. It reports whether there was one to take.
func (l *limiter) allow(key string, rate float64, burst float64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &bucket{tokens: burst - 1, last: now}
		return true
	}

	b.tokens = min(burst, b.tokens+now.Sub(b.last).Seconds()*rate)
	b.last = now

	if b.tokens < 1 {
		return false
	}
	b.tokens--

	return true
}

// clientIP is the address the rate limit counts against.
//
// Caddy is the only thing that talks to this process -- it listens on
// loopback -- so X-Forwarded-For is written by us. Without reading it at all,
// every request would look like it came from 127.0.0.1 and one bucket would
// serve the whole internet.
//
// It is the LAST entry that matters, not the first. Caddy appends the address
// it observed to whatever the request already carried, so a caller who sends
// "X-Forwarded-For: 1.2.3.4" produces "1.2.3.4, <their real address>". Reading
// the first entry would let anybody pick their own rate-limit bucket, and
// change it on every request -- which is the same as having no limit. The last
// entry is the only one this system wrote.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.LastIndex(xff, ","); i >= 0 {
			return strings.TrimSpace(xff[i+1:])
		}
		return strings.TrimSpace(xff)
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
