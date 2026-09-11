// Package store is the whole persistence layer: one SQLite file holding the
// flower commitments, the event's settings, and the admin's sign-in state.
//
// There is no encryption at rest and no backup, and both are deliberate. What
// is stored is a list of names of people who offered to bring flowers to a
// shrine, for five weeks, on an instance that is then destroyed. See
// docs/scope.md.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // the cgo-free driver, registered as "sqlite"
)

// Store owns the database handle. One of these exists per process.
type Store struct {
	db *sql.DB
}

// The tables are STRICT, which makes SQLite reject a value of the wrong type
// instead of quietly storing it. SQLite's default affinity rules would let a
// bug write the string "null" into deleted_at and call it a date; on a schema
// this small the strictness costs nothing and removes a class of surprise.
//
// Times are RFC3339 in UTC. Text rather than an integer because somebody will
// open this file with the sqlite3 CLI at some point during the event and needs
// to read it without arithmetic — and in UTC the text sorts chronologically,
// which is the only property the queries need.
const schema = `
CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS commitments (
	id            INTEGER PRIMARY KEY,
	event_id      TEXT NOT NULL,
	name          TEXT NOT NULL,
	browser_token TEXT NOT NULL,
	created_at    TEXT NOT NULL,
	deleted_at    TEXT
) STRICT;

CREATE INDEX IF NOT EXISTS commitments_by_event
	ON commitments(event_id, deleted_at);
CREATE INDEX IF NOT EXISTS commitments_by_browser
	ON commitments(event_id, browser_token, deleted_at);

CREATE TABLE IF NOT EXISTS admin_links (
	token_hash TEXT PRIMARY KEY,
	email      TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	used_at    TEXT
) STRICT;

CREATE TABLE IF NOT EXISTS admin_sessions (
	token_hash TEXT PRIMARY KEY,
	email      TEXT NOT NULL,
	created_at TEXT NOT NULL,
	expires_at TEXT NOT NULL
) STRICT;
`

// Open opens the database at path, creating it if it is not there, and applies
// the schema. Pass ":memory:" in tests.
func Open(ctx context.Context, path string) (*Store, error) {
	// The pragmas go in the DSN because this driver has no other hook that
	// runs on every new connection, and the pool opens more than one.
	//
	// WAL so a reader — the widget asking for the count — is never blocked by
	// the writer. busy_timeout so that when two commitments do land in the
	// same instant, the loser waits rather than returning SQLITE_BUSY to a
	// guest who pressed a button.
	dsn := fmt.Sprintf(
		"file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"+
			"&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)",
		path,
	)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening the database at %s: %w", path, err)
	}

	// One writer. SQLite allows exactly one at a time regardless, and letting
	// the pool believe otherwise converts a short wait into a busy error.
	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("reaching the database at %s: %w", path, err)
	}

	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying the schema: %w", err)
	}

	st := &Store{db: db}
	if err := st.ensureEventID(ctx, defaultEvent().ID); err != nil {
		db.Close()
		return nil, err
	}

	return st, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

// formatTime and parseTime are the only two places that decide how a time is
// spelled on disk. Keeping them together is what makes the choice reversible.
func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}
