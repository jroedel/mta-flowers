package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/jroedel/mta-flowers/internal/mail"
	"github.com/jroedel/mta-flowers/internal/store"
)

// state is what the widget renders from. One request answers everything it
// needs to draw itself, because two would mean a visible flicker between the
// count arriving and the button knowing whether it has been pressed.
type state struct {
	EventID      string `json:"event_id"`
	DateLine     string `json:"date_line"`
	Instructions string `json:"instructions"`
	Goal         int    `json:"goal"`
	Count        int    `json:"count"`

	// Committed and Name describe this browser, and are the reason the widget
	// can say "You are bringing flowers, Maria" on a second visit.
	Committed bool   `json:"committed"`
	Name      string `json:"name,omitempty"`
}

// browserToken identifies a browser, and is deliberately not a cookie.
//
// Last year's widget used one, and a cookie set by this server inside a page
// on schoenstatt-austin.us is a third-party cookie. Safari has blocked those
// by default for years and Chrome restricts them, so the "you already signed
// up" memory would work for some guests and not others, with no pattern
// anybody could explain.
//
// So the widget generates a random token, keeps it in localStorage -- which is
// first-party to the Squarespace page and has none of those problems -- and
// sends it here. It is not a credential: it identifies a browser to itself, it
// is trivially changed, and it guards nothing but a double-tap.
func browserToken(r *http.Request) string {
	return r.Header.Get("X-Flower-Browser")
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// A real query, not a bare 200. The deploy workflow rolls back on this,
	// and a health check that cannot fail is a health check that cannot roll
	// anything back.
	if _, err := s.store.EventID(r.Context()); err != nil {
		s.log.Error("the health check could not reach the database", "error", err)
		http.Error(w, "unhealthy", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte("ok\n"))
}

func (s *Server) handlePreflight(w http.ResponseWriter, r *http.Request) {
	s.allowOrigin(w, r)
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Flower-Browser")
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWidgetScript(w http.ResponseWriter, r *http.Request) {
	b, err := assets.ReadFile("assets/flowers.js")
	if err != nil {
		s.log.Error("the widget script is missing from the binary", "error", err)
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	// Short, because the whole point of serving this ourselves is being able
	// to fix the widget during the event without waiting for a cache.
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write(b)
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	s.allowOrigin(w, r)

	st, err := s.currentState(r)
	if err != nil {
		s.log.Error("building the widget state", "error", err)
		writeJSON(w, http.StatusInternalServerError,
			map[string]string{"error": "The flowers page is having trouble. Please try again in a moment."})
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleCommit(w http.ResponseWriter, r *http.Request) {
	s.allowOrigin(w, r)

	if !s.limits.allow("commit:"+clientIP(r), 0.2, 5) {
		writeJSON(w, http.StatusTooManyRequests,
			map[string]string{"error": "That is a lot of signing up at once. Please wait a moment."})
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "Please type a name before pressing the button."})
		return
	}

	token := browserToken(r)
	if token == "" {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "Your browser did not send enough information. Please reload the page."})
		return
	}

	eventID, err := s.store.EventID(r.Context())
	if err != nil {
		s.serverTrouble(w, "reading the event id", err)
		return
	}

	c, err := s.store.Add(r.Context(), eventID, body.Name, token)
	switch {
	case err == nil:
		// carry on

	case isInvalidName(err):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return

	default:
		if already, ok := errors.AsType[store.AlreadyCommitted](err); ok {
			// Not an error the guest should see as one: they pressed twice, or
			// came back to the page. Answer with the state, which makes the
			// widget draw its "you are already down" face.
			st, sErr := s.currentState(r)
			if sErr != nil {
				s.serverTrouble(w, "building the state after a repeat commitment", sErr)
				return
			}
			st.Committed = true
			st.Name = already.Existing.Name
			writeJSON(w, http.StatusOK, st)
			return
		}

		s.serverTrouble(w, "recording a commitment", err)
		return
	}

	count, err := s.store.Count(r.Context(), eventID)
	if err != nil {
		s.log.Error("counting after a commitment", "error", err)
	}

	s.notify(mail.Committed(c.Name, s.eventName(), c.CreatedAt, count))

	st, err := s.currentState(r)
	if err != nil {
		s.serverTrouble(w, "building the state after a commitment", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	s.allowOrigin(w, r)

	if !s.limits.allow("commit:"+clientIP(r), 0.2, 5) {
		writeJSON(w, http.StatusTooManyRequests,
			map[string]string{"error": "Please wait a moment and try again."})
		return
	}

	token := browserToken(r)
	if token == "" {
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "Your browser did not send enough information. Please reload the page."})
		return
	}

	eventID, err := s.store.EventID(r.Context())
	if err != nil {
		s.serverTrouble(w, "reading the event id", err)
		return
	}

	c, err := s.store.Cancel(r.Context(), eventID, token)
	if err != nil {
		// Nothing to cancel is not a failure worth an error page: the widget
		// and the database simply disagree, and the state below settles it.
		st, sErr := s.currentState(r)
		if sErr != nil {
			s.serverTrouble(w, "building the state after a cancellation", sErr)
			return
		}
		writeJSON(w, http.StatusOK, st)
		return
	}

	count, err := s.store.Count(r.Context(), eventID)
	if err != nil {
		s.log.Error("counting after a cancellation", "error", err)
	}

	s.notify(mail.Cancelled(c.Name, s.eventName(), time.Now(), count))

	st, err := s.currentState(r)
	if err != nil {
		s.serverTrouble(w, "building the state after a cancellation", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) currentState(r *http.Request) (state, error) {
	ctx := r.Context()

	ev, err := s.store.Event(ctx)
	if err != nil {
		return state{}, err
	}

	count, err := s.store.Count(ctx, ev.ID)
	if err != nil {
		return state{}, err
	}

	st := state{
		EventID:      ev.ID,
		DateLine:     ev.DateLine(),
		Instructions: ev.Instructions,
		Goal:         ev.Goal,
		Count:        count,
	}

	if token := browserToken(r); token != "" {
		if mine, err := s.store.CommitmentByBrowser(ctx, ev.ID, token); err == nil {
			st.Committed = true
			st.Name = mine.Name
		}
	}

	return st, nil
}

// notify sends to the organisers, if there are any and mail is configured.
// It never blocks the guest -- see mail.SendAsync.
func (s *Server) notify(m mail.Message) {
	if len(s.cfg.NotifyRecipients) == 0 {
		return
	}
	m.To = s.cfg.NotifyRecipients
	s.mail.SendAsync(m)
}

func (s *Server) eventName() string {
	if s.cfg.EventName != "" {
		return s.cfg.EventName
	}
	return "Feast Day Celebration"
}

// serverTrouble logs the real reason and tells the guest something useful.
// The two are deliberately different: the log names the operation, the page
// says what to do.
func (s *Server) serverTrouble(w http.ResponseWriter, doing string, err error) {
	s.log.Error(doing, "error", err)
	writeJSON(w, http.StatusInternalServerError,
		map[string]string{"error": "The flowers page is having trouble. Please try again in a moment."})
}

func isInvalidName(err error) bool {
	_, ok := errors.AsType[store.InvalidName](err)
	return ok
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
