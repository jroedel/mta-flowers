package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	netmail "net/mail"
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

// Who is told
//
// The notification list lives here rather than in the environment, because
// docs/scope.md leaves "who receives the notifications" open and answering it
// should not be a deploy. NOTIFY_RECIPIENTS is still read at startup, but only
// to fill a database that has never had a list -- see SeedNotifyRecipients.
// Once an organiser has saved one on the admin page, the environment is not
// consulted again: otherwise an address removed in the browser would come back
// at the next restart, and the person who removed it would never learn why.
//
// The value is stored the way the environment spells it, comma-separated on
// one line, so that a file opened with the sqlite3 CLI and the credential file
// say the same thing in the same shape.
const notifyRecipientsKey = "notify_recipients"

// maxRecipients guards against the failure this list can cause rather than
// marking a limit anybody should reach. Every commitment sends one message to
// every address, so a hundred commitments and twenty addresses is two thousand
// messages through one submission account -- which is the sort of thing a mail
// provider rate-limits, and the notification that is then dropped is the one
// that mattered. If more people than this want telling, a digest is the answer
// and docs/scope.md already carries the question.
const maxRecipients = 20

// InvalidRecipients is returned when a notification list cannot be used. Its
// message is shown on the admin form, so it names the entry at fault.
type InvalidRecipients struct {
	Message string
}

func (e InvalidRecipients) Error() string { return e.Message }

// ParseRecipients reads a list of addresses the way a person types one:
// separated by commas or by newlines, with blank entries and stray spaces
// ignored. An empty list is a legitimate answer -- it means nobody asked to be
// told, and the widget still works.
//
// Each address is reduced to the bare form, so "Maria <m@x.org>" pasted out of
// a mail client is kept as "m@x.org". The display form is friendly in a To:
// header and wrong in an SMTP RCPT command, and mail.Sender uses these same
// strings for both.
func ParseRecipients(raw string) ([]string, error) {
	var out []string
	seen := map[string]bool{}

	for _, entry := range recipientEntries(raw) {
		addr, err := netmail.ParseAddress(entry)
		if err != nil {
			return nil, InvalidRecipients{Message: fmt.Sprintf(
				"%q does not look like an email address. Put one address on each line.", entry)}
		}

		// Quietly, rather than as a refusal: the same address twice is a
		// paste, not a mistake worth stopping for, and the only thing it would
		// otherwise achieve is two copies of every notification.
		if key := NormaliseEmail(addr.Address); !seen[key] {
			seen[key] = true
			out = append(out, addr.Address)
		}
	}

	if len(out) > maxRecipients {
		return nil, InvalidRecipients{Message: fmt.Sprintf(
			"That is %d addresses, and %d is as many as this can send to. "+
				"Everybody on the list gets an email for every single commitment.",
			len(out), maxRecipients)}
	}

	return out, nil
}

// recipientEntries splits the typed or stored form into one entry per address.
// Trimming afterwards is what handles a carriage return from a textarea.
func recipientEntries(raw string) []string {
	var out []string
	for line := range strings.SplitSeq(raw, "\n") {
		for part := range strings.SplitSeq(line, ",") {
			if entry := strings.TrimSpace(part); entry != "" {
				out = append(out, entry)
			}
		}
	}
	return out
}

// NotifyRecipients is who hears about every commitment and cancellation.
//
// Reading is deliberately more forgiving than saving: an entry that will not
// parse is dropped and the rest are still told. Everything in this row was
// written by SaveNotifyRecipients or SeedNotifyRecipients and has been checked
// once already, so the only way to get a bad one in is to edit the file by
// hand during the event -- and on that day losing one address is a far smaller
// thing than a dashboard that will not open and notifications that all stop.
func (s *Store) NotifyRecipients(ctx context.Context) ([]string, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, notifyRecipientsKey,
	).Scan(&raw)

	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading who is notified: %w", err)
	}

	var out []string
	for _, entry := range recipientEntries(raw) {
		if addr, err := netmail.ParseAddress(entry); err == nil {
			out = append(out, addr.Address)
		}
	}
	return out, nil
}

// SaveNotifyRecipients replaces the list with what was typed on the admin
// page, and returns the list as it was stored. An empty list is saved as an
// empty list: "tell nobody" is a choice an organiser is allowed to make, and
// it must not be mistaken later for a database that was never configured.
func (s *Store) SaveNotifyRecipients(ctx context.Context, raw string) ([]string, error) {
	list, err := ParseRecipients(raw)
	if err != nil {
		return nil, err
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		notifyRecipientsKey, strings.Join(list, ","),
	); err != nil {
		return nil, fmt.Errorf("saving who is notified: %w", err)
	}

	return list, nil
}

// SeedNotifyRecipients writes the environment's list into a database that has
// never had one, and leaves any existing list alone. It returns the list that
// is actually in force, which is what the startup log reports.
//
// An empty raw value writes nothing at all rather than writing an empty list.
// The difference matters on the first start of a server whose credential file
// is not filled in yet: nothing written means filling it in later still works,
// where an empty list written now would make the environment silently
// irrelevant for ever after.
func (s *Store) SeedNotifyRecipients(ctx context.Context, raw string) ([]string, error) {
	list, err := ParseRecipients(raw)
	if err != nil {
		return nil, err
	}

	if len(list) > 0 {
		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO NOTHING`,
			notifyRecipientsKey, strings.Join(list, ","),
		); err != nil {
			return nil, fmt.Errorf("setting who is notified: %w", err)
		}
	}

	return s.NotifyRecipients(ctx)
}
