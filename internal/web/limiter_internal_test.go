package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A caller who can choose their own rate-limit bucket has no rate limit, since
// they can pick a fresh one on every request. Caddy appends the address it
// observed, so the last entry is the only trustworthy one.
func TestTheRateLimitBucketCannotBeChosenByTheCaller(t *testing.T) {
	cases := map[string]string{
		// what the header holds            // whose bucket it must count against
		"203.0.113.7":                   "203.0.113.7",
		"1.2.3.4, 203.0.113.7":          "203.0.113.7",
		"  1.2.3.4 ,  203.0.113.7  ":    "203.0.113.7",
		"1.2.3.4, 5.6.7.8, 203.0.113.7": "203.0.113.7",
	}

	for header, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		r.Header.Set("X-Forwarded-For", header)

		if got := clientIP(r); got != want {
			t.Errorf("X-Forwarded-For %q counted against %q, want %q", header, got, want)
		}
	}
}

func TestWithoutAProxyHeaderTheConnectionAddressIsUsed(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	r.RemoteAddr = "203.0.113.7:54321"

	if got := clientIP(r); got != "203.0.113.7" {
		t.Errorf("clientIP = %q, want the address without the port", got)
	}
}

func TestTheLimitEventuallySaysNo(t *testing.T) {
	l := newLimiter()

	// A burst of three, refilling slowly enough that the fourth is refused.
	var allowed int
	for range 10 {
		if l.allow("someone", 0.001, 3) {
			allowed++
		}
	}

	if allowed != 3 {
		t.Errorf("%d of 10 requests were allowed through a burst of 3", allowed)
	}

	// A different caller has their own bucket and is unaffected.
	if !l.allow("somebody-else", 0.001, 3) {
		t.Error("one caller hitting the limit blocked a different caller")
	}
}
