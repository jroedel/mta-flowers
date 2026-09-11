package web

import (
	"crypto/subtle"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/jroedel/mta-flowers/internal/mail"
	"github.com/jroedel/mta-flowers/internal/store"
)

const sessionCookie = "flowers_admin"

// requireAdmin wraps the pages that change things.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}

		email, err := s.store.Session(r.Context(), c.Value)
		if err != nil {
			if !errors.Is(err, store.ErrNoSession) {
				s.log.Error("reading a session", "error", err)
			}
			s.clearSession(w)
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}

		// The allowlist is checked on every request, not only at sign-in.
		// Removing somebody from ADMIN_EMAILS and redeploying should end their
		// access, and a sixty-day session would otherwise outlive the change.
		if !s.isAdminEmail(email) {
			s.log.Warn("a session belongs to an address no longer on the allowlist", "email", email)
			s.clearSession(w)
			http.Redirect(w, r, "/admin", http.StatusSeeOther)
			return
		}

		// Every mutating page is a form post, so the origin check belongs here
		// rather than in each handler.
		if r.Method == http.MethodPost && !s.sameOrigin(r) {
			http.Error(w, "This form was not submitted from the flowers page.", http.StatusForbidden)
			return
		}

		next(w, r)
	}
}

func (s *Server) redirectToAdmin(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleSignInPage(w http.ResponseWriter, r *http.Request) {
	// Somebody already signed in should not have to sign in again.
	if c, err := r.Cookie(sessionCookie); err == nil {
		if email, err := s.store.Session(r.Context(), c.Value); err == nil && s.isAdminEmail(email) {
			http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
			return
		}
	}

	s.render(w, "signin.html", map[string]any{
		"Sent":        r.URL.Query().Get("sent") != "",
		"Problem":     r.URL.Query().Get("problem"),
		"HasFallback": s.cfg.FallbackPassword != "",
		"MailWorking": s.mail.Configured(),
	})
}

func (s *Server) handleSendLink(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		http.Error(w, "This form was not submitted from the flowers page.", http.StatusForbidden)
		return
	}

	// Slow enough that somebody's inbox cannot be filled from here, generous
	// enough that mistyping an address twice is not a lockout.
	if !s.limits.allow("signin:"+clientIP(r), 0.05, 5) {
		http.Redirect(w, r, "/admin?problem=Please+wait+a+moment+and+try+again.", http.StatusSeeOther)
		return
	}

	email := store.NormaliseEmail(r.FormValue("email"))

	// The answer is the same whether or not the address is allowed. An
	// unauthenticated page that distinguishes them is a page that tells a
	// stranger which parish addresses are administrators.
	if s.isAdminEmail(email) {
		token, err := s.store.CreateSignInLink(r.Context(), email)
		if err != nil {
			s.log.Error("creating a sign-in link", "error", err)
			http.Redirect(w, r, "/admin?problem=Something+went+wrong.+Please+try+again.", http.StatusSeeOther)
			return
		}

		m := mail.SignIn(fmt.Sprintf("%s/admin/enter?token=%s", s.cfg.PublicURL, token), store.LinkValidFor)
		m.To = []string{email}
		s.mail.SendAsync(m)
	} else {
		s.log.Info("a sign-in link was asked for by an address not on the allowlist", "email", email)
	}

	http.Redirect(w, r, "/admin?sent=1", http.StatusSeeOther)
}

func (s *Server) handleEnter(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")

	email, session, err := s.store.RedeemSignInLink(r.Context(), token)
	if err != nil {
		if !errors.Is(err, store.ErrNoSession) {
			s.log.Error("redeeming a sign-in link", "error", err)
		}
		http.Redirect(w, r,
			"/admin?problem=That+link+has+been+used+or+has+expired.+Please+ask+for+another.",
			http.StatusSeeOther)
		return
	}

	if !s.isAdminEmail(email) {
		http.Redirect(w, r, "/admin?problem=That+address+is+no+longer+an+administrator.", http.StatusSeeOther)
		return
	}

	s.setSession(w, session)
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

// handlePasswordSignIn is the way in on a day when mail is not working, which
// is exactly the day somebody needs to get in. Without it the magic link would
// be a single point of failure that depends on somebody else's mail server.
func (s *Server) handlePasswordSignIn(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) {
		http.Error(w, "This form was not submitted from the flowers page.", http.StatusForbidden)
		return
	}

	if s.cfg.FallbackPassword == "" {
		http.Redirect(w, r, "/admin?problem=The+fallback+password+is+not+set+up.", http.StatusSeeOther)
		return
	}

	// Slower than the link: this is the one endpoint where guessing gets
	// somebody in, so it is the one that has to be tedious to attack.
	if !s.limits.allow("password:"+clientIP(r), 0.02, 5) {
		http.Redirect(w, r, "/admin?problem=Too+many+attempts.+Please+wait+a+few+minutes.", http.StatusSeeOther)
		return
	}

	// Constant time, so the number of correct leading characters cannot be
	// read off the response time.
	given := r.FormValue("password")
	if subtle.ConstantTimeCompare([]byte(given), []byte(s.cfg.FallbackPassword)) != 1 {
		s.log.Warn("a wrong fallback password was tried", "ip", clientIP(r))
		http.Redirect(w, r, "/admin?problem=That+password+is+not+right.", http.StatusSeeOther)
		return
	}

	email := r.FormValue("email")
	if !s.isAdminEmail(email) {
		http.Redirect(w, r, "/admin?problem=That+address+is+not+an+administrator.", http.StatusSeeOther)
		return
	}

	session, err := s.store.CreateSession(r.Context(), email)
	if err != nil {
		s.log.Error("creating a session from the fallback password", "error", err)
		http.Redirect(w, r, "/admin?problem=Something+went+wrong.+Please+try+again.", http.StatusSeeOther)
		return
	}

	s.log.Warn("somebody signed in with the fallback password", "email", email, "ip", clientIP(r))
	s.setSession(w, session)
	http.Redirect(w, r, "/admin/dashboard", http.StatusSeeOther)
}

func (s *Server) handleSignOut(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.store.EndSession(r.Context(), c.Value); err != nil {
			s.log.Error("signing out", "error", err)
		}
	}
	s.clearSession(w)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ev, err := s.store.Event(ctx)
	if err != nil {
		s.adminTrouble(w, "reading the event", err)
		return
	}

	all, err := s.store.List(ctx, ev.ID, true)
	if err != nil {
		s.adminTrouble(w, "listing commitments", err)
		return
	}

	var active, removed []store.Commitment
	seen := map[string]bool{}
	duplicates := map[int64]bool{}
	for _, c := range all {
		if c.Cancelled() {
			removed = append(removed, c)
			continue
		}
		// Flag the second and later appearance of a name, so the duplicates
		// the admin came here to delete are marked rather than hunted for.
		if seen[c.Name] {
			duplicates[c.ID] = true
		}
		seen[c.Name] = true
		active = append(active, c)
	}

	s.render(w, "dashboard.html", map[string]any{
		"Event":       ev,
		"DateValue":   ev.Date.Format(time.DateOnly),
		"Active":      active,
		"Removed":     removed,
		"Duplicates":  duplicates,
		"Count":       len(active),
		"Notice":      r.URL.Query().Get("notice"),
		"Problem":     r.URL.Query().Get("problem"),
		"MailWorking": s.mail.Configured(),
	})
}

func (s *Server) handleSaveEvent(w http.ResponseWriter, r *http.Request) {
	ev, err := s.store.Event(r.Context())
	if err != nil {
		s.adminTrouble(w, "reading the event", err)
		return
	}

	if d := r.FormValue("date"); d != "" {
		parsed, err := time.Parse(time.DateOnly, d)
		if err != nil {
			s.back(w, r, "problem", "That date could not be read. Please pick one from the calendar.")
			return
		}
		ev.Date = parsed
	}
	if g := r.FormValue("goal"); g != "" {
		n, err := strconv.Atoi(g)
		if err != nil {
			s.back(w, r, "problem", "The number of people to ask for must be a whole number.")
			return
		}
		ev.Goal = n
	}
	ev.Instructions = r.FormValue("instructions")

	if err := s.store.SaveEvent(r.Context(), ev); err != nil {
		if invalid, ok := errors.AsType[store.InvalidEvent](err); ok {
			s.back(w, r, "problem", invalid.Message)
			return
		}
		s.adminTrouble(w, "saving the event", err)
		return
	}

	s.back(w, r, "notice", "Saved.")
}

func (s *Server) handleRemove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		s.back(w, r, "problem", "That commitment could not be found.")
		return
	}

	if err := s.store.SoftDelete(r.Context(), id); err != nil {
		s.back(w, r, "problem", err.Error())
		return
	}

	s.back(w, r, "notice", "Removed. You can put it back below.")
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err != nil {
		s.back(w, r, "problem", "That commitment could not be found.")
		return
	}

	if err := s.store.Restore(r.Context(), id); err != nil {
		s.back(w, r, "problem", err.Error())
		return
	}

	s.back(w, r, "notice", "Put back.")
}

func (s *Server) handleReset(w http.ResponseWriter, r *http.Request) {
	ev, err := s.store.Event(r.Context())
	if err != nil {
		s.adminTrouble(w, "reading the event", err)
		return
	}

	n, err := s.store.Reset(r.Context(), ev.ID)
	if err != nil {
		s.adminTrouble(w, "resetting the count", err)
		return
	}

	s.log.Warn("the count was reset", "removed", n)
	s.back(w, r, "notice", fmt.Sprintf(
		"The count is back to zero. All %d are listed under Removed and can be put back.", n))
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	ev, err := s.store.Event(ctx)
	if err != nil {
		s.adminTrouble(w, "reading the event", err)
		return
	}

	// Everything, cancelled rows included. This file is the only copy that
	// outlives the instance, so it is not the place to be selective.
	all, err := s.store.List(ctx, ev.ID, true)
	if err != nil {
		s.adminTrouble(w, "listing commitments for export", err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="flowers-%s.csv"`, ev.Date.Format(time.DateOnly)))

	cw := csv.NewWriter(w)
	defer cw.Flush()

	if err := cw.Write([]string{"name", "committed_at", "status", "removed_at"}); err != nil {
		s.log.Error("writing the export header", "error", err)
		return
	}

	for _, c := range all {
		status, removed := "active", ""
		if c.Cancelled() {
			status, removed = "removed", localTime(*c.DeletedAt)
		}
		if err := cw.Write([]string{c.Name, localTime(c.CreatedAt), status, removed}); err != nil {
			s.log.Error("writing the export", "error", err)
			return
		}
	}
}

func (s *Server) setSession(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		// Lax rather than Strict: the magic link arrives from a mail client,
		// which is a cross-site navigation, and Strict would drop the cookie
		// on exactly the request that just set it. Lax still refuses to send
		// it on a cross-site form post, which is the attack this guards.
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(store.SessionValidFor),
	})
}

func (s *Server) clearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// back returns to the dashboard with one message. Redirecting rather than
// rendering in place is what stops a refresh from repeating the action.
func (s *Server) back(w http.ResponseWriter, r *http.Request, kind, message string) {
	u := "/admin/dashboard?" + kind + "=" + urlQueryEscape(message)
	http.Redirect(w, r, u, http.StatusSeeOther)
}

func (s *Server) adminTrouble(w http.ResponseWriter, doing string, err error) {
	s.log.Error(doing, "error", err)
	http.Error(w, "Something went wrong. Please try again.", http.StatusInternalServerError)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("rendering a page", "template", name, "error", err)
	}
}
