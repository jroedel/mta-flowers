package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// Commitment is one person's promise to bring flowers.
type Commitment struct {
	ID        int64
	EventID   string
	Name      string
	CreatedAt time.Time

	// DeletedAt is set when an organiser removes the row or a guest cancels.
	// Nothing is ever deleted outright — see SoftDelete.
	DeletedAt *time.Time
}

// Cancelled reports whether this commitment has been withdrawn or removed.
func (c Commitment) Cancelled() bool { return c.DeletedAt != nil }

// InvalidName is returned when a name cannot be stored as typed. Its message
// is written for the guest and is shown to them word for word, so it says what
// to do rather than what went wrong.
type InvalidName struct {
	Message string
}

func (e InvalidName) Error() string { return e.Message }

// AlreadyCommitted is returned when this browser has an active commitment for
// this event. It carries the existing name so the widget can say "you are
// already down as Maria" rather than refusing blankly.
type AlreadyCommitted struct {
	Existing Commitment
}

func (e AlreadyCommitted) Error() string {
	return fmt.Sprintf("this browser is already committed as %q", e.Existing.Name)
}

// maxNameLen is generous on purpose. It exists to stop a paste of an entire
// document, not to have an opinion about anybody's name — a long compound
// surname, a religious name, and a couple bringing flowers together all have
// to fit without an argument.
const maxNameLen = 120

// CleanName trims a typed name and reports why it cannot be used, if it
// cannot. It is exported because the widget's handler validates before writing
// and the admin's edit form validates the same way, and two spellings of this
// rule would eventually disagree.
func CleanName(raw string) (string, error) {
	// Collapse every run of whitespace to one space. Phone keyboards produce
	// trailing spaces constantly, and "Maria  Schmidt" and "Maria Schmidt"
	// arriving as two different people would make the duplicate list useless.
	//
	// This also flattens newlines rather than refusing them, which is
	// deliberate twice over. A stray Return on a phone should not be an error
	// message. And the cleaned name goes into the subject line of a
	// notification email, where a surviving newline would be header injection
	// -- so this is the guard for that, and TestACleanedNameIsAlwaysOneLine
	// holds it in place.
	name := strings.Join(strings.Fields(raw), " ")

	if name == "" {
		return "", InvalidName{Message: "Please type a name before pressing the button."}
	}

	// Count runes, not bytes: a name in Vietnamese or with several accents is
	// not longer than one in English, and len() would say it was.
	if len([]rune(name)) > maxNameLen {
		return "", InvalidName{Message: "That name is too long — please shorten it."}
	}

	// Control characters cannot be typed by accident and have no business in a
	// name. They reach a template, a CSV file and an email body, and each of
	// those has its own way of being surprised by them.
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", InvalidName{Message: "Please use letters only — that name has characters we cannot print."}
		}
	}

	return name, nil
}

// Add records a commitment. browserToken identifies the browser, so the same
// person pressing twice is refused rather than counted twice; it is politeness
// and not enforcement, since anybody can clear a cookie.
func (s *Store) Add(ctx context.Context, eventID, rawName, browserToken string) (Commitment, error) {
	name, err := CleanName(rawName)
	if err != nil {
		return Commitment{}, err
	}

	// Checked and inserted in one transaction. Without it, two taps a
	// millisecond apart — which is what a double-tap on a phone is — both see
	// no existing row and both insert.
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Commitment{}, fmt.Errorf("starting the transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has run

	existing, err := commitmentByBrowser(ctx, tx, eventID, browserToken)
	switch {
	case err == nil:
		return Commitment{}, AlreadyCommitted{Existing: existing}
	case !errors.Is(err, sql.ErrNoRows):
		return Commitment{}, err
	}

	now := time.Now()
	res, err := tx.ExecContext(ctx,
		`INSERT INTO commitments (event_id, name, browser_token, created_at)
		 VALUES (?, ?, ?, ?)`,
		eventID, name, browserToken, formatTime(now),
	)
	if err != nil {
		return Commitment{}, fmt.Errorf("recording the commitment: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Commitment{}, fmt.Errorf("reading back the new commitment: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Commitment{}, fmt.Errorf("committing: %w", err)
	}

	return Commitment{
		ID:        id,
		EventID:   eventID,
		Name:      name,
		CreatedAt: now.UTC().Truncate(time.Nanosecond),
	}, nil
}

// Cancel withdraws the active commitment held by this browser. It returns the
// commitment as it was, so the notification can name it.
func (s *Store) Cancel(ctx context.Context, eventID, browserToken string) (Commitment, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Commitment{}, fmt.Errorf("starting the transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has run

	existing, err := commitmentByBrowser(ctx, tx, eventID, browserToken)
	if err != nil {
		return Commitment{}, err
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE commitments SET deleted_at = ? WHERE id = ?`,
		formatTime(time.Now()), existing.ID,
	); err != nil {
		return Commitment{}, fmt.Errorf("cancelling the commitment: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Commitment{}, fmt.Errorf("committing: %w", err)
	}

	return existing, nil
}

// Count is the number under the button: active commitments for this event.
func (s *Store) Count(ctx context.Context, eventID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM commitments WHERE event_id = ? AND deleted_at IS NULL`,
		eventID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("counting commitments: %w", err)
	}
	return n, nil
}

// List returns the commitments for an event, newest first. Cancelled rows are
// included only when asked for, because the admin page needs to show them to
// offer the undo.
func (s *Store) List(ctx context.Context, eventID string, includeCancelled bool) ([]Commitment, error) {
	q := `SELECT id, event_id, name, created_at, deleted_at
	        FROM commitments
	       WHERE event_id = ?`
	if !includeCancelled {
		q += ` AND deleted_at IS NULL`
	}
	q += ` ORDER BY created_at DESC, id DESC`

	rows, err := s.db.QueryContext(ctx, q, eventID)
	if err != nil {
		return nil, fmt.Errorf("listing commitments: %w", err)
	}
	defer rows.Close()

	var out []Commitment
	for rows.Next() {
		c, err := scanCommitment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading the commitment list: %w", err)
	}

	return out, nil
}

// SoftDelete removes one commitment from the count without losing the row.
//
// The admin actions that reach this — deleting a duplicate, resetting the
// count — will at some point be done in a hurry on a phone on the morning of
// the feast. A DELETE would make that permanent; a column makes it a mistake
// somebody can walk back. A hundred names cost nothing to keep.
func (s *Store) SoftDelete(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE commitments SET deleted_at = ? WHERE id = ? AND deleted_at IS NULL`,
		formatTime(time.Now()), id,
	)
	if err != nil {
		return fmt.Errorf("removing the commitment: %w", err)
	}
	return mustAffectOne(res, "That commitment is not there, or was already removed.")
}

// Restore puts a soft-deleted commitment back. This is the undo.
func (s *Store) Restore(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE commitments SET deleted_at = NULL WHERE id = ? AND deleted_at IS NOT NULL`,
		id,
	)
	if err != nil {
		return fmt.Errorf("restoring the commitment: %w", err)
	}
	return mustAffectOne(res, "That commitment is not there, or was never removed.")
}

// Reset soft-deletes every active commitment for an event and reports how many
// it touched. Undone one row at a time through the admin list.
func (s *Store) Reset(ctx context.Context, eventID string) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE commitments SET deleted_at = ?
		  WHERE event_id = ? AND deleted_at IS NULL`,
		formatTime(time.Now()), eventID,
	)
	if err != nil {
		return 0, fmt.Errorf("resetting the count: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("counting what the reset touched: %w", err)
	}
	return int(n), nil
}

// commitmentByBrowser finds this browser's active commitment. It returns
// sql.ErrNoRows when there is none, which both callers treat as a real answer
// rather than a failure.
func commitmentByBrowser(ctx context.Context, tx *sql.Tx, eventID, browserToken string) (Commitment, error) {
	row := tx.QueryRowContext(ctx,
		`SELECT id, event_id, name, created_at, deleted_at
		   FROM commitments
		  WHERE event_id = ? AND browser_token = ? AND deleted_at IS NULL
		  ORDER BY id DESC
		  LIMIT 1`,
		eventID, browserToken,
	)

	c, err := scanCommitment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Commitment{}, err // the caller decides what no rows means
	}
	return c, err
}

// scanner is what QueryRow and Rows have in common, so one scan function
// serves both.
type scanner interface {
	Scan(dest ...any) error
}

func scanCommitment(sc scanner) (Commitment, error) {
	var (
		c         Commitment
		created   string
		deletedAt sql.NullString
	)

	if err := sc.Scan(&c.ID, &c.EventID, &c.Name, &created, &deletedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Commitment{}, err
		}
		return Commitment{}, fmt.Errorf("reading a commitment: %w", err)
	}

	t, err := parseTime(created)
	if err != nil {
		return Commitment{}, fmt.Errorf("reading the time on commitment %d: %w", c.ID, err)
	}
	c.CreatedAt = t

	if deletedAt.Valid {
		d, err := parseTime(deletedAt.String)
		if err != nil {
			return Commitment{}, fmt.Errorf("reading the cancellation time on commitment %d: %w", c.ID, err)
		}
		c.DeletedAt = &d
	}

	return c, nil
}

// mustAffectOne turns "the UPDATE matched nothing" into an error a person can
// read, which is otherwise a silent success.
func mustAffectOne(res sql.Result, message string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("checking what changed: %w", err)
	}
	if n == 0 {
		return errors.New(message)
	}
	return nil
}

// CommitmentByBrowser returns this browser's active commitment, so the widget
// can greet somebody who has already signed up by name. It reports
// sql.ErrNoRows when there is none, which is the ordinary case.
func (s *Store) CommitmentByBrowser(ctx context.Context, eventID, browserToken string) (Commitment, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Commitment{}, fmt.Errorf("starting the transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // read-only

	return commitmentByBrowser(ctx, tx, eventID, browserToken)
}
