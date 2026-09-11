package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// LinkValidFor is how long a magic link works. Fifteen minutes is long
	// enough to walk to a different device and short enough that a link left
	// in an inbox is not a standing key.
	LinkValidFor = 15 * time.Minute

	// SessionValidFor outlives the event, so nobody is asked to sign in again
	// on the morning of the feast.
	SessionValidFor = 60 * 24 * time.Hour
)

// ErrNoSession is returned when a token does not identify a live session or an
// unused link. It is the ordinary answer for an expired cookie and is not
// logged as a failure.
var ErrNoSession = errors.New("not signed in")

// newToken returns a secret to put in a URL or a cookie, and its hash.
//
// Only the hash is stored. A database that leaks then hands over nothing that
// can be used to sign in -- which matters more than it sounds for a file that
// will be copied to a laptop as a CSV export at some point during the event.
func newToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generating a token: %w", err)
	}

	// URL-safe and unpadded, because this goes in a query string and a "="
	// there survives every mail client differently.
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NormaliseEmail lowercases and trims an address so that the allowlist matches
// what somebody types on a phone, which capitalises the first letter.
func NormaliseEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// CreateSignInLink records a single-use token for email and returns the secret
// to put in the link. The caller has already checked the allowlist.
func (s *Store) CreateSignInLink(ctx context.Context, email string) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}

	now := time.Now()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO admin_links (token_hash, email, created_at, expires_at)
		 VALUES (?, ?, ?, ?)`,
		hash, NormaliseEmail(email), formatTime(now), formatTime(now.Add(LinkValidFor)),
	); err != nil {
		return "", fmt.Errorf("recording the sign-in link: %w", err)
	}

	return token, nil
}

// RedeemSignInLink turns a link token into a session token. The link is marked
// used in the same transaction that reads it, so a link forwarded to somebody
// else -- or fetched twice by a mail client that follows links to preview them
// -- cannot produce a second session.
func (s *Store) RedeemSignInLink(ctx context.Context, token string) (email, sessionToken string, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", "", fmt.Errorf("starting the transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has run

	var expires string
	row := tx.QueryRowContext(ctx,
		`SELECT email, expires_at FROM admin_links
		  WHERE token_hash = ? AND used_at IS NULL`,
		hashToken(token),
	)
	if err := row.Scan(&email, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", ErrNoSession
		}
		return "", "", fmt.Errorf("reading the sign-in link: %w", err)
	}

	exp, err := parseTime(expires)
	if err != nil {
		return "", "", fmt.Errorf("reading the link's expiry: %w", err)
	}
	if time.Now().After(exp) {
		return "", "", ErrNoSession
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE admin_links SET used_at = ? WHERE token_hash = ?`,
		formatTime(time.Now()), hashToken(token),
	); err != nil {
		return "", "", fmt.Errorf("marking the link used: %w", err)
	}

	sessionToken, err = createSession(ctx, tx, email)
	if err != nil {
		return "", "", err
	}

	if err := tx.Commit(); err != nil {
		return "", "", fmt.Errorf("committing: %w", err)
	}

	return email, sessionToken, nil
}

// CreateSession signs somebody in without a link. The fallback password uses
// this, which is the way back in on a day when mail is not working.
func (s *Store) CreateSession(ctx context.Context, email string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("starting the transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has run

	token, err := createSession(ctx, tx, email)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("committing: %w", err)
	}

	return token, nil
}

func createSession(ctx context.Context, tx *sql.Tx, email string) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}

	now := time.Now()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO admin_sessions (token_hash, email, created_at, expires_at)
		 VALUES (?, ?, ?, ?)`,
		hash, NormaliseEmail(email), formatTime(now), formatTime(now.Add(SessionValidFor)),
	); err != nil {
		return "", fmt.Errorf("starting the session: %w", err)
	}

	return token, nil
}

// Session returns the email behind a session token, or ErrNoSession.
func (s *Store) Session(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", ErrNoSession
	}

	var email, expires string
	err := s.db.QueryRowContext(ctx,
		`SELECT email, expires_at FROM admin_sessions WHERE token_hash = ?`,
		hashToken(token),
	).Scan(&email, &expires)

	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNoSession
	}
	if err != nil {
		return "", fmt.Errorf("reading the session: %w", err)
	}

	exp, err := parseTime(expires)
	if err != nil {
		return "", fmt.Errorf("reading the session's expiry: %w", err)
	}
	if time.Now().After(exp) {
		return "", ErrNoSession
	}

	return email, nil
}

// EndSession signs somebody out.
func (s *Store) EndSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM admin_sessions WHERE token_hash = ?`, hashToken(token),
	); err != nil {
		return fmt.Errorf("signing out: %w", err)
	}
	return nil
}

// PurgeExpired clears out spent links and dead sessions. Nothing depends on it
// running -- every read checks the expiry itself -- so it is housekeeping,
// called on a timer, and its failure is worth a log line and nothing more.
func (s *Store) PurgeExpired(ctx context.Context) error {
	now := formatTime(time.Now())

	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM admin_links WHERE expires_at < ? OR used_at IS NOT NULL`, now,
	); err != nil {
		return fmt.Errorf("clearing spent sign-in links: %w", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM admin_sessions WHERE expires_at < ?`, now,
	); err != nil {
		return fmt.Errorf("clearing expired sessions: %w", err)
	}

	return nil
}
