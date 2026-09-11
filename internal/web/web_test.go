package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jroedel/mta-flowers/internal/mail"
	"github.com/jroedel/mta-flowers/internal/store"
	"github.com/jroedel/mta-flowers/internal/web"
)

const squarespace = "https://schoenstatt-austin.us"

func newServer(t *testing.T) http.Handler {
	t.Helper()

	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// An unconfigured Sender, so no test ever sends mail. SendAsync logs and
	// returns, which is exactly what a development machine does.
	srv, err := web.New(st, mail.Sender{Log: discardLogger()}, web.Config{
		PublicURL:        "https://flowers.schoenstatt.link",
		AllowedOrigins:   []string{squarespace},
		AdminEmails:      []string{"frjeff@schoenstatt.us"},
		FallbackPassword: "a-test-password",
		NotifyRecipients: []string{"organiser@example.org"},
	}, discardLogger())
	if err != nil {
		t.Fatalf("building the server: %v", err)
	}

	return srv.Handler()
}

func commit(t *testing.T, h http.Handler, browser, name string) *httptest.ResponseRecorder {
	t.Helper()

	body := strings.NewReader(`{"name":` + quote(name) + `}`)
	r := httptest.NewRequest(http.MethodPost, "/api/commit", body)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Flower-Browser", browser)
	r.Header.Set("Origin", squarespace)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func decodeState(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()

	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding %q: %v", w.Body.String(), err)
	}
	return got
}

func TestPressingTheButtonCountsOne(t *testing.T) {
	h := newServer(t)

	w := commit(t, h, "browser-1", "Maria Schmidt")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}

	got := decodeState(t, w)
	if got["count"] != float64(1) {
		t.Errorf("count = %v, want 1", got["count"])
	}
	if got["committed"] != true {
		t.Errorf("committed = %v, want true", got["committed"])
	}
	if got["name"] != "Maria Schmidt" {
		t.Errorf("name = %v", got["name"])
	}
}

// A double tap must look to the guest exactly like a successful single tap.
// An error here would be a guest who thinks the sign-up is broken.
func TestADoubleTapLooksLikeSuccess(t *testing.T) {
	h := newServer(t)

	commit(t, h, "browser-1", "Maria Schmidt")
	w := commit(t, h, "browser-1", "Maria Schmidt")

	if w.Code != http.StatusOK {
		t.Fatalf("the second tap answered %d, want 200: %s", w.Code, w.Body)
	}
	got := decodeState(t, w)
	if got["count"] != float64(1) {
		t.Errorf("count = %v after a double tap, want 1", got["count"])
	}
	if got["committed"] != true {
		t.Error("the second tap did not report the guest as committed")
	}
}

func TestAnEmptyNameIsRefusedWithAdvice(t *testing.T) {
	h := newServer(t)

	w := commit(t, h, "browser-1", "   ")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}

	var got map[string]string
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["error"] == "" {
		t.Fatal("no message for the guest")
	}
	if !strings.HasSuffix(got["error"], ".") {
		t.Errorf("%q is not a sentence", got["error"])
	}
}

func TestCancellingPutsTheCountBack(t *testing.T) {
	h := newServer(t)
	commit(t, h, "browser-1", "Maria Schmidt")

	r := httptest.NewRequest(http.MethodPost, "/api/cancel", nil)
	r.Header.Set("X-Flower-Browser", "browser-1")
	r.Header.Set("Origin", squarespace)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body)
	}
	got := decodeState(t, w)
	if got["count"] != float64(0) {
		t.Errorf("count = %v after cancelling, want 0", got["count"])
	}
	if got["committed"] != false {
		t.Errorf("committed = %v after cancelling, want false", got["committed"])
	}
}

// The allowlist is the only thing standing between this API and any site on
// the internet posting commitments from a visitor's browser.
func TestOnlyTheParishSiteIsGivenCORSPermission(t *testing.T) {
	h := newServer(t)

	cases := map[string]string{
		squarespace:                              squarespace,
		"https://evil.example.org":               "",
		"https://schoenstatt-austin.us.evil.org": "",
	}

	for origin, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if got := w.Header().Get("Access-Control-Allow-Origin"); got != want {
			t.Errorf("origin %s got Access-Control-Allow-Origin %q, want %q", origin, got, want)
		}
	}
}

func TestTheWidgetScriptIsServedFromTheBinary(t *testing.T) {
	h := newServer(t)

	r := httptest.NewRequest(http.MethodGet, "/flowers.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	// The two globals the Squarespace page sets. If a rename ever loses one,
	// the widget silently stops finding its API.
	for _, want := range []string{"FLOWERS_API", "flowers-widget"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("the script no longer mentions %q", want)
		}
	}
}

func TestHealthzAnswers(t *testing.T) {
	h := newServer(t)

	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	// The deploy workflow rolls back on anything but 200.
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 -- every deploy depends on this", w.Code)
	}
}

// Every page that changes something must be unreachable without a session.
func TestTheAdminPagesAreClosed(t *testing.T) {
	h := newServer(t)

	paths := map[string]string{
		"GET":  "/admin/dashboard",
		"POST": "/admin/reset",
	}

	for method, path := range paths {
		r := httptest.NewRequest(method, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != http.StatusSeeOther {
			t.Errorf("%s %s = %d, want a redirect to the sign-in page", method, path, w.Code)
		}
		if loc := w.Header().Get("Location"); loc != "/admin" {
			t.Errorf("%s %s redirected to %q", method, path, loc)
		}
	}

	// The export is the whole list of names. It is the one worth naming twice.
	r := httptest.NewRequest(http.MethodGet, "/admin/export.csv", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("the CSV export answered %d without a session", w.Code)
	}
}

// An unauthenticated page that answers differently for a real administrator
// than for a stranger tells a stranger which addresses are administrators.
func TestTheSignInPageDoesNotRevealWhoIsAnAdministrator(t *testing.T) {
	h := newServer(t)

	var bodies []string
	for _, email := range []string{"frjeff@schoenstatt.us", "stranger@example.org"} {
		r := httptest.NewRequest(http.MethodPost, "/admin/link",
			strings.NewReader("email="+email))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://flowers.schoenstatt.link")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s: status = %d", email, w.Code)
		}
		bodies = append(bodies, w.Header().Get("Location"))
	}

	if bodies[0] != bodies[1] {
		t.Errorf("an administrator is sent to %q and a stranger to %q", bodies[0], bodies[1])
	}
}

// SameSite=Lax already refuses a cross-site form post, but a second lock costs
// one header read and survives a future change to the cookie.
func TestAFormPostFromAnotherSiteIsRefused(t *testing.T) {
	h := newServer(t)

	r := httptest.NewRequest(http.MethodPost, "/admin/link", strings.NewReader("email=x@y.org"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://evil.example.org")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusForbidden {
		t.Errorf("a cross-site post answered %d, want 403", w.Code)
	}
}

func TestTheFallbackPasswordLetsAnOrganiserIn(t *testing.T) {
	h := newServer(t)

	form := "email=frjeff@schoenstatt.us&password=a-test-password"
	r := httptest.NewRequest(http.MethodPost, "/admin/password", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://flowers.schoenstatt.link")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Header().Get("Location") != "/admin/dashboard" {
		t.Fatalf("signing in went to %q: %s", w.Header().Get("Location"), w.Body)
	}

	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "flowers_admin" {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no session cookie was set")
	}
	if !session.HttpOnly || !session.Secure {
		t.Errorf("the session cookie is HttpOnly=%v Secure=%v; both must be true",
			session.HttpOnly, session.Secure)
	}
	if session.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", session.SameSite)
	}

	// And it actually opens the dashboard.
	r2 := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	r2.AddCookie(session)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)

	if w2.Code != http.StatusOK {
		t.Fatalf("the dashboard answered %d with a valid session", w2.Code)
	}
	if !strings.Contains(w2.Body.String(), "Reset the count") {
		t.Error("the dashboard does not offer the reset")
	}
}

func TestTheWrongFallbackPasswordIsRefused(t *testing.T) {
	h := newServer(t)

	form := "email=frjeff@schoenstatt.us&password=wrong"
	r := httptest.NewRequest(http.MethodPost, "/admin/password", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://flowers.schoenstatt.link")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if loc := w.Header().Get("Location"); strings.Contains(loc, "dashboard") {
		t.Fatal("a wrong password signed somebody in")
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "flowers_admin" && c.Value != "" {
			t.Error("a wrong password set a session cookie")
		}
	}
}

// A name is echoed back into an HTML page. This is the one place it could
// become markup.
func TestANameCannotBecomeMarkupOnTheDashboard(t *testing.T) {
	h := newServer(t)
	commit(t, h, "browser-1", `<script>alert(1)</script>`)

	form := "email=frjeff@schoenstatt.us&password=a-test-password"
	r := httptest.NewRequest(http.MethodPost, "/admin/password", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://flowers.schoenstatt.link")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "flowers_admin" {
			session = c
		}
	}
	if session == nil {
		t.Fatal("could not sign in")
	}

	r2 := httptest.NewRequest(http.MethodGet, "/admin/dashboard", nil)
	r2.AddCookie(session)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)

	if strings.Contains(w2.Body.String(), "<script>alert(1)</script>") {
		t.Error("a guest's name reached the page as live markup")
	}
	if !strings.Contains(w2.Body.String(), "&lt;script&gt;") {
		t.Error("the name does not appear escaped either; is it on the page at all?")
	}
}

// Firefox sends the literal string "null" as the Origin of a same-origin form
// post when the page carries a strict Referrer-Policy, and sends no Referer to
// correlate it with. The first version of sameOrigin compared that against the
// real URL and refused -- so the organiser signing in on the real page was
// told the form had not come from it.
//
// This is a lock-out bug rather than a hole, which makes it the worse kind:
// nothing is exposed, and the person who needs to fix it cannot get in.
func TestFirefoxCanSignIn(t *testing.T) {
	h := newServer(t)

	r := httptest.NewRequest(http.MethodPost, "/admin/link",
		strings.NewReader("email=frjeff@schoenstatt.us"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// Exactly what Firefox 155 sent, taken from the server's access log.
	r.Header.Set("Origin", "null")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Sec-Fetch-Mode", "navigate")

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code == http.StatusForbidden {
		t.Fatal("a same-origin sign-in from Firefox was refused as cross-site")
	}
	if w.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want 303", w.Code)
	}
}

// Sec-Fetch-Site is set by the browser and cannot be forged by a page, so it
// must be believed over an Origin header that disagrees with it.
func TestACrossSiteFetchIsStillRefusedHoweverItLabelsItself(t *testing.T) {
	cases := map[string]map[string]string{
		"cross-site":                 {"Sec-Fetch-Site": "cross-site"},
		"same-site but another host": {"Sec-Fetch-Site": "same-site"},
		"a forged Origin with an honest Sec-Fetch-Site": {
			"Sec-Fetch-Site": "cross-site",
			"Origin":         "https://flowers.schoenstatt.link",
		},
		"no fetch metadata, wrong origin":  {"Origin": "https://evil.example.org"},
		"no fetch metadata, wrong referer": {"Referer": "https://evil.example.org/x"},
	}

	for what, headers := range cases {
		r := httptest.NewRequest(http.MethodPost, "/admin/link",
			strings.NewReader("email=frjeff@schoenstatt.us"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range headers {
			r.Header.Set(k, v)
		}

		w := httptest.NewRecorder()
		h := newServer(t)
		h.ServeHTTP(w, r)

		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", what, w.Code)
		}
	}
}

// The token is in the query string of the link. The policy has to keep it away
// from other people's servers -- but no-referrer is what produced the Firefox
// lock-out above, so the weaker policy that still does the job is the right
// one and is worth pinning.
func TestTheReferrerPolicyProtectsTheTokenWithoutBreakingSignIn(t *testing.T) {
	h := newServer(t)

	r := httptest.NewRequest(http.MethodGet, "/admin", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	got := w.Header().Get("Referrer-Policy")
	if got != "same-origin" {
		t.Errorf("Referrer-Policy = %q, want same-origin (no-referrer makes Firefox send Origin: null)", got)
	}
}

// The Squarespace page carries one snippet, not two. If the widget ever needs
// telling where its API is again, the setup grows a step that can silently
// half-succeed -- which is how it failed the first time it went on the site.
func TestTheWidgetFindsItsOwnApi(t *testing.T) {
	h := newServer(t)

	r := httptest.NewRequest(http.MethodGet, "/flowers.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	body := w.Body.String()
	for _, want := range []string{"document.currentScript", "ownOrigin()"} {
		if !strings.Contains(body, want) {
			t.Errorf("the widget no longer works out its own origin: missing %q", want)
		}
	}
}
