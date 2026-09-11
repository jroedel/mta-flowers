package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Event is everything about the celebration that an organiser can change
// without a deploy: when it is, how many people are being asked for, and the
// two sentences of instructions the widget shows.
type Event struct {
	// ID identifies the event in the database and never changes, even when the
	// date does. It is the "church-flowers-2026-10-17" the Squarespace page
	// carries in window.FLOWERS_EVENT_ID.
	//
	// Deriving it from the date instead was the obvious first idea and is
	// wrong: moving the celebration by a day would then orphan every
	// commitment already made, on the day when the count matters most and
	// nobody has time to work out why it went to zero.
	ID string

	// Date is the day itself, shown to guests. Changing it is one of the three
	// things the admin page exists for.
	Date time.Time

	// Goal is the number being asked for -- 100 last year. Shown as progress,
	// never enforced; nobody is turned away for being the hundred and first.
	Goal int

	// Instructions is the sentence under the heading, in the organiser's own
	// words. Last year: arrive by 10am and leave the flowers behind the
	// communion rail in the Shrine.
	Instructions string
}

// DateLine is the date as the widget writes it: "Sat, Oct 17".
func (e Event) DateLine() string { return e.Date.Format("Mon, Jan 2") }

// Defaults are what a fresh database starts with, so the widget renders
// something sensible before anybody has opened the admin page. They are this
// year's real values; only the instructions are a guess and the open question
// is in docs/scope.md.
func defaultEvent() Event {
	return Event{
		ID:   "church-flowers-2026-10-17",
		Date: time.Date(2026, time.October, 17, 0, 0, 0, 0, time.UTC),
		Goal: 100,
		Instructions: "Please arrive by 10 am and leave the flowers behind " +
			"the communion rail in the Shrine.",
	}
}

// Event reads the current settings, filling in any key that has never been
// written with its default.
func (s *Store) Event(ctx context.Context) (Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return Event{}, fmt.Errorf("reading the event settings: %w", err)
	}
	defer rows.Close()

	stored := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return Event{}, fmt.Errorf("reading an event setting: %w", err)
		}
		stored[k] = v
	}
	if err := rows.Err(); err != nil {
		return Event{}, fmt.Errorf("reading the event settings: %w", err)
	}

	// A stored value that will not parse is treated as absent rather than
	// fatal. A malformed goal should cost the progress line, not the button.
	ev := defaultEvent()
	if v, ok := stored["event_id"]; ok && v != "" {
		ev.ID = v
	}
	if v, ok := stored["event_date"]; ok {
		if d, err := time.Parse(time.DateOnly, v); err == nil {
			ev.Date = d
		}
	}
	if v, ok := stored["goal"]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ev.Goal = n
		}
	}
	if v, ok := stored["instructions"]; ok && v != "" {
		ev.Instructions = v
	}

	return ev, nil
}

// InvalidEvent is returned when a change from the admin form cannot be
// applied. Its message is shown on the form.
type InvalidEvent struct {
	Message string
}

func (e InvalidEvent) Error() string { return e.Message }

// SaveEvent writes the settings an organiser changed. ID is not writable here:
// it is set once when the database is created and changing it would hide every
// existing commitment behind a new identity.
func (s *Store) SaveEvent(ctx context.Context, ev Event) error {
	instructions := strings.TrimSpace(ev.Instructions)

	switch {
	case ev.Date.IsZero():
		return InvalidEvent{Message: "Please choose a date for the celebration."}
	case ev.Goal < 1:
		return InvalidEvent{Message: "The number of people to ask for must be at least one."}
	case instructions == "":
		return InvalidEvent{Message: "Please write the instructions guests will see."}
	case len([]rune(instructions)) > 500:
		return InvalidEvent{Message: "Those instructions are too long for the widget — please shorten them."}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting the transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has run

	values := map[string]string{
		"event_date":   ev.Date.Format(time.DateOnly),
		"goal":         strconv.Itoa(ev.Goal),
		"instructions": instructions,
	}
	for k, v := range values {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			k, v,
		); err != nil {
			return fmt.Errorf("saving the %s setting: %w", k, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing: %w", err)
	}
	return nil
}

// ensureEventID writes the event id exactly once, on a database that has never
// had one. Open calls it, so the id in the table always matches what the
// widget was told, and later reads do not depend on the default.
func (s *Store) ensureEventID(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES ('event_id', ?)
		 ON CONFLICT(key) DO NOTHING`,
		id,
	)
	if err != nil {
		return fmt.Errorf("setting the event id: %w", err)
	}
	return nil
}

// EventID is the identity every commitment is filed under.
func (s *Store) EventID(ctx context.Context) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = 'event_id'`,
	).Scan(&id)

	if errors.Is(err, sql.ErrNoRows) {
		return defaultEvent().ID, nil
	}
	if err != nil {
		return "", fmt.Errorf("reading the event id: %w", err)
	}
	return id, nil
}
