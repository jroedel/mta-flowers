// Package web is everything a browser touches: the widget script and its two
// endpoints, and the organiser's admin pages.
//
// The two halves have different security models and it is worth being explicit
// about why, because it looks inconsistent otherwise.
//
// The widget runs on schoenstatt-austin.us and calls this server on
// flowers.schoenstatt.link, so it is cross-origin. It therefore uses no
// cookies at all -- see browserToken -- and its endpoints are guarded by an
// origin allowlist and a rate limit.
//
// The admin pages are served from this server directly, so they are
// first-party and use an ordinary session cookie.
package web

import (
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jroedel/mta-flowers/internal/mail"
	"github.com/jroedel/mta-flowers/internal/store"
)

//go:embed assets/*
var assets embed.FS

//go:embed templates/*.html
var templates embed.FS

// Config is everything the handlers need that is not the database.
type Config struct {
	// PublicURL is this server's own base, used to build the sign-in link.
	// Without a trailing slash.
	PublicURL string

	// AllowedOrigins are the sites permitted to embed the widget. The
	// Squarespace page, and nothing else.
	AllowedOrigins []string

	// AdminEmails may sign in. An address not here never receives a link.
	AdminEmails []string

	// FallbackPassword signs an organiser in without mail working. Empty
	// disables it.
	FallbackPassword string

	// EventName is what the notifications call the celebration.
	EventName string
}

// Server holds the handlers' dependencies.
type Server struct {
	store  *store.Store
	mail   mail.Sender
	cfg    Config
	log    *slog.Logger
	tmpl   *template.Template
	limits *limiter
}

// New builds the server and parses the templates, which fails now rather than
// on the first request that needs one.
func New(st *store.Store, sender mail.Sender, cfg Config, log *slog.Logger) (*Server, error) {
	tmpl, err := template.New("").Funcs(template.FuncMap{
		"localTime": localTime,
	}).ParseFS(templates, "templates/*.html")
	if err != nil {
		return nil, err
	}

	return &Server{
		store:  st,
		mail:   sender,
		cfg:    cfg,
		log:    log,
		tmpl:   tmpl,
		limits: newLimiter(),
	}, nil
}

// Handler is the whole routing table.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public, called by the widget from another origin.
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /flowers.js", s.handleWidgetScript)
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/commit", s.handleCommit)
	mux.HandleFunc("POST /api/cancel", s.handleCancel)
	mux.HandleFunc("OPTIONS /api/", s.handlePreflight)

	// The organiser's pages, first-party and cookie-authenticated.
	mux.HandleFunc("GET /", s.redirectToAdmin)
	mux.HandleFunc("GET /admin", s.handleSignInPage)
	mux.HandleFunc("POST /admin/link", s.handleSendLink)
	mux.HandleFunc("POST /admin/password", s.handlePasswordSignIn)
	mux.HandleFunc("GET /admin/enter", s.handleEnter)
	mux.HandleFunc("POST /admin/signout", s.handleSignOut)

	mux.HandleFunc("GET /admin/dashboard", s.requireAdmin(s.handleDashboard))
	mux.HandleFunc("POST /admin/event", s.requireAdmin(s.handleSaveEvent))
	mux.HandleFunc("POST /admin/recipients", s.requireAdmin(s.handleSaveRecipients))
	mux.HandleFunc("POST /admin/remove", s.requireAdmin(s.handleRemove))
	mux.HandleFunc("POST /admin/restore", s.requireAdmin(s.handleRestore))
	mux.HandleFunc("POST /admin/reset", s.requireAdmin(s.handleReset))
	mux.HandleFunc("GET /admin/export.csv", s.requireAdmin(s.handleExport))

	return s.withRequestLog(s.withSecurityHeaders(mux))
}

// withSecurityHeaders sets the few that matter here.
func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")

		// A magic link arrives as a query string, and without a policy the
		// first outbound request from the page it lands on would carry that
		// token to somebody else's server in the Referer header.
		//
		// same-origin, not no-referrer. no-referrer keeps the token in just
		// the same way, but it also makes Firefox send "Origin: null" on a
		// same-origin form post and send no Referer to correlate it with --
		// which left sameOrigin with nothing true to look at, and returned
		// "This form was not submitted from the flowers page" to an organiser
		// signing in on the real page. same-origin still sends nothing to
		// anybody else.
		w.Header().Set("Referrer-Policy", "same-origin")

		next.ServeHTTP(w, r)
	})
}

func (s *Server) withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		// A panic in a handler must not take the process down with it. This is
		// one binary serving one event; a nil map in the admin list should
		// cost that request and nothing else.
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("a handler panicked",
					"method", r.Method, "path", r.URL.Path, "panic", v)
				http.Error(rec, "Something went wrong. Please try again.",
					http.StatusInternalServerError)
			}

			s.log.Info("request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"ms", time.Since(start).Milliseconds())
		}()

		next.ServeHTTP(rec, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.wroteHeader {
		return
	}
	r.status = code
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	return r.ResponseWriter.Write(b)
}

// allowOrigin echoes the request's origin when it is on the allowlist.
//
// Echoing the matched origin rather than answering "*" is what lets the
// allowlist mean anything: "*" would let any site on the internet embed the
// widget and post commitments. Vary tells caches that the answer depends on
// who asked.
func (s *Server) allowOrigin(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Vary", "Origin")

	origin := r.Header.Get("Origin")
	if origin == "" {
		return
	}
	for _, allowed := range s.cfg.AllowedOrigins {
		if strings.EqualFold(origin, allowed) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			return
		}
	}
}

// sameOrigin guards the admin form posts.
//
// The session cookie is SameSite=Lax, which already stops a cross-site form
// post from carrying it. This is the second lock: it costs one header read,
// and it means a future change to the cookie's attributes cannot silently
// remove the only protection.
//
// It reads three headers because no one of them is present everywhere, and
// getting this wrong locks out the administrator rather than an attacker.
// That is not hypothetical -- the first version compared Origin alone and
// refused Firefox, which sends the literal string "null" for a same-origin
// form post when the page carries a strict Referrer-Policy. See the
// Referrer-Policy comment in withSecurityHeaders.
func (s *Server) sameOrigin(r *http.Request) bool {
	// Sec-Fetch-Site is set by the browser and is on the forbidden-header
	// list, so a page cannot forge it. Where it exists it is the best answer
	// available, and it is the one header that was correct in the case above.
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none": // "none" is a typed URL or a bookmark
		return true
	case "same-site", "cross-site":
		return false
	}

	// Older browsers. "null" is not an origin -- it is a browser declining to
	// name one -- so it must not be compared as though it were.
	if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
		return strings.EqualFold(origin, s.cfg.PublicURL)
	}

	if ref := r.Header.Get("Referer"); ref != "" {
		return strings.HasPrefix(ref, s.cfg.PublicURL+"/")
	}

	// No usable signal at all. Accepting is the right call: a browser this old
	// omits all three on an ordinary same-origin post, the session cookie is
	// still SameSite=Lax, and the alternative is an organiser who simply
	// cannot sign in and has no way to find out why.
	return true
}

func (s *Server) isAdminEmail(email string) bool {
	email = store.NormaliseEmail(email)
	for _, a := range s.cfg.AdminEmails {
		if store.NormaliseEmail(a) == email {
			return true
		}
	}
	return false
}

func localTime(t time.Time) string {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		loc = time.UTC
	}
	return t.In(loc).Format("Mon 2 Jan, 3:04 PM")
}

func urlQueryEscape(s string) string { return url.QueryEscape(s) }
