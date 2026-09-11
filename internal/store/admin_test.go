package store_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jroedel/mta-flowers/internal/store"
)

func TestALinkSignsSomebodyIn(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	token, err := s.CreateSignInLink(ctx, "frjeff@schoenstatt.us")
	if err != nil {
		t.Fatalf("creating the link: %v", err)
	}

	email, session, err := s.RedeemSignInLink(ctx, token)
	if err != nil {
		t.Fatalf("redeeming: %v", err)
	}
	if email != "frjeff@schoenstatt.us" {
		t.Errorf("signed in as %q", email)
	}

	got, err := s.Session(ctx, session)
	if err != nil {
		t.Fatalf("reading the session back: %v", err)
	}
	if got != "frjeff@schoenstatt.us" {
		t.Errorf("session belongs to %q", got)
	}
}

// A mail client that follows links to generate a preview will fetch this once
// before the organiser ever clicks it. Single use is what stops that from
// being a problem for the person, and what stops a forwarded mail from being
// a standing key.
func TestALinkWorksOnlyOnce(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	token, _ := s.CreateSignInLink(ctx, "frjeff@schoenstatt.us")
	if _, _, err := s.RedeemSignInLink(ctx, token); err != nil {
		t.Fatalf("first redemption: %v", err)
	}

	_, _, err := s.RedeemSignInLink(ctx, token)
	if !errors.Is(err, store.ErrNoSession) {
		t.Errorf("second redemption returned %v, want ErrNoSession", err)
	}
}

func TestAnInventedTokenIsRefused(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if _, _, err := s.RedeemSignInLink(ctx, "not-a-real-token"); !errors.Is(err, store.ErrNoSession) {
		t.Errorf("an invented link returned %v, want ErrNoSession", err)
	}
	if _, err := s.Session(ctx, "not-a-real-token"); !errors.Is(err, store.ErrNoSession) {
		t.Errorf("an invented session returned %v, want ErrNoSession", err)
	}
	if _, err := s.Session(ctx, ""); !errors.Is(err, store.ErrNoSession) {
		t.Errorf("an empty session returned %v, want ErrNoSession", err)
	}
}

func TestSigningOutEndsTheSession(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	session, err := s.CreateSession(ctx, "frjeff@schoenstatt.us")
	if err != nil {
		t.Fatalf("creating the session: %v", err)
	}
	if err := s.EndSession(ctx, session); err != nil {
		t.Fatalf("signing out: %v", err)
	}
	if _, err := s.Session(ctx, session); !errors.Is(err, store.ErrNoSession) {
		t.Errorf("the session survived sign-out: %v", err)
	}
}

func TestAnAddressMatchesHoweverItWasTyped(t *testing.T) {
	for _, in := range []string{"FrJeff@Schoenstatt.us", "  frjeff@schoenstatt.us  ", "FRJEFF@SCHOENSTATT.US"} {
		if got := store.NormaliseEmail(in); got != "frjeff@schoenstatt.us" {
			t.Errorf("NormaliseEmail(%q) = %q", in, got)
		}
	}
}

// The database is exported as CSV and copied to a laptop during the event. If
// a live token were in it, that export would be a set of keys.
func TestTheDatabaseNeverHoldsAUsableToken(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	link, _ := s.CreateSignInLink(ctx, "frjeff@schoenstatt.us")
	session, _ := s.CreateSession(ctx, "frjeff@schoenstatt.us")

	for _, table := range []string{"admin_links", "admin_sessions"} {
		stored, err := s.DebugColumn(ctx, table, "token_hash")
		if err != nil {
			t.Fatalf("reading %s: %v", table, err)
		}
		for _, v := range stored {
			if v == link || v == session {
				t.Errorf("%s stores a token verbatim; a leaked database would be a set of keys", table)
			}
			if strings.Contains(v, link) || strings.Contains(v, session) {
				t.Errorf("%s stores something containing a live token", table)
			}
		}
	}
}
