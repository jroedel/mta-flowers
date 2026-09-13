package store_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jroedel/mta-flowers/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()

	// A real file rather than :memory:, because that is what production uses
	// and because WAL behaves differently in memory. t.TempDir cleans it up.
	s, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening the store: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	return s
}

const event = "church-flowers-2026-10-17"

func TestACommitmentIsCountedOnce(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if _, err := s.Add(ctx, event, "Maria Schmidt", "browser-1"); err != nil {
		t.Fatalf("adding: %v", err)
	}

	n, err := s.Count(ctx, event)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d, want 1", n)
	}
}

// The double-tap. This is the failure a guest is most likely to produce: a
// phone that registers two touches on one press.
func TestTheSameBrowserCannotCommitTwice(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if _, err := s.Add(ctx, event, "Maria Schmidt", "browser-1"); err != nil {
		t.Fatalf("first add: %v", err)
	}

	_, err := s.Add(ctx, event, "Maria Schmidt", "browser-1")
	already, ok := errors.AsType[store.AlreadyCommitted](err)
	if !ok {
		t.Fatalf("second add returned %v, want AlreadyCommitted", err)
	}
	if already.Existing.Name != "Maria Schmidt" {
		t.Errorf("the refusal names %q, want it to name the existing commitment", already.Existing.Name)
	}

	n, _ := s.Count(ctx, event)
	if n != 1 {
		t.Errorf("count = %d after a double tap, want 1", n)
	}
}

// The same thing from two goroutines, which is the double-tap without the
// millisecond of luck that makes the sequential version pass.
func TestConcurrentTapsFromOneBrowserProduceOneCommitment(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			_, _ = s.Add(ctx, event, "Maria Schmidt", "browser-1")
		})
	}
	wg.Wait()

	n, err := s.Count(ctx, event)
	if err != nil {
		t.Fatalf("counting: %v", err)
	}
	if n != 1 {
		t.Errorf("count = %d after eight concurrent taps, want 1", n)
	}
}

func TestDifferentBrowsersEachCount(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	for _, b := range []string{"browser-1", "browser-2", "browser-3"} {
		if _, err := s.Add(ctx, event, "Somebody", b); err != nil {
			t.Fatalf("adding for %s: %v", b, err)
		}
	}

	n, _ := s.Count(ctx, event)
	if n != 3 {
		t.Errorf("count = %d, want 3", n)
	}
}

func TestCancellingFreesTheBrowserToCommitAgain(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if _, err := s.Add(ctx, event, "Maria Schmidt", "browser-1"); err != nil {
		t.Fatalf("adding: %v", err)
	}

	cancelled, err := s.Cancel(ctx, event, "browser-1")
	if err != nil {
		t.Fatalf("cancelling: %v", err)
	}
	if cancelled.Name != "Maria Schmidt" {
		t.Errorf("cancel returned %q, want the commitment it withdrew", cancelled.Name)
	}

	if n, _ := s.Count(ctx, event); n != 0 {
		t.Errorf("count = %d after cancelling, want 0", n)
	}

	// Somebody who cancels by mistake must be able to sign up again.
	if _, err := s.Add(ctx, event, "Maria Schmidt", "browser-1"); err != nil {
		t.Errorf("re-adding after a cancellation: %v", err)
	}
	if n, _ := s.Count(ctx, event); n != 1 {
		t.Error("re-adding after a cancellation did not restore the count")
	}
}

// The undo. A reset on the morning of the feast must be recoverable, which is
// the entire reason deleted_at exists instead of a DELETE.
func TestResetIsUndoneOneRowAtATime(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	for _, b := range []string{"b1", "b2", "b3"} {
		if _, err := s.Add(ctx, event, "Somebody", b); err != nil {
			t.Fatalf("adding: %v", err)
		}
	}

	n, err := s.Reset(ctx, event)
	if err != nil {
		t.Fatalf("resetting: %v", err)
	}
	if n != 3 {
		t.Errorf("reset reported %d rows, want 3", n)
	}
	if c, _ := s.Count(ctx, event); c != 0 {
		t.Errorf("count = %d after a reset, want 0", c)
	}

	// The rows are still there, which is what makes the undo possible.
	all, err := s.List(ctx, event, true)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("a reset lost rows: %d remain, want 3", len(all))
	}
	for _, c := range all {
		if !c.Cancelled() {
			t.Errorf("commitment %d survived the reset as active", c.ID)
		}
	}

	if err := s.Restore(ctx, all[0].ID); err != nil {
		t.Fatalf("restoring: %v", err)
	}
	if c, _ := s.Count(ctx, event); c != 1 {
		t.Errorf("count = %d after restoring one, want 1", c)
	}
}

func TestDeletingADuplicateLeavesTheOriginal(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	first, _ := s.Add(ctx, event, "Maria Schmidt", "b1")
	dup, _ := s.Add(ctx, event, "Maria Schmidt", "b2")

	if err := s.SoftDelete(ctx, dup.ID); err != nil {
		t.Fatalf("deleting the duplicate: %v", err)
	}

	active, err := s.List(ctx, event, false)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	if len(active) != 1 || active[0].ID != first.ID {
		t.Errorf("active list = %+v, want only the original", active)
	}
}

func TestDeletingTwiceSaysSoRatherThanSucceedingQuietly(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	c, _ := s.Add(ctx, event, "Maria Schmidt", "b1")
	if err := s.SoftDelete(ctx, c.ID); err != nil {
		t.Fatalf("first delete: %v", err)
	}

	err := s.SoftDelete(ctx, c.ID)
	if err == nil {
		t.Fatal("deleting an already-deleted commitment reported success")
	}
	if strings.Contains(err.Error(), "store") || strings.Contains(err.Error(), "sql") {
		t.Errorf("the message names a Go package, which reaches a person: %q", err)
	}
}

func TestNamesAreTidiedTheSameWayEveryTime(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Maria Schmidt", "Maria Schmidt"},
		{"  Maria Schmidt  ", "Maria Schmidt"},
		{"Maria   Schmidt", "Maria Schmidt"},
		{"Maria\tSchmidt", "Maria Schmidt"},
		{"María Ruiz de la Peña", "María Ruiz de la Peña"},
		{"Nguyễn Thị Ánh", "Nguyễn Thị Ánh"},
	}

	for _, tc := range cases {
		got, err := store.CleanName(tc.in)
		if err != nil {
			t.Errorf("CleanName(%q) refused it: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("CleanName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAnUnusableNameIsRefusedWithAdviceForTheGuest(t *testing.T) {
	cases := map[string]string{
		"empty":    "",
		"spaces":   "   ",
		"too long": strings.Repeat("a", 200),
		"control":  "Maria\x00Schmidt",
	}

	for what, in := range cases {
		_, err := store.CleanName(in)
		invalid, ok := errors.AsType[store.InvalidName](err)
		if !ok {
			t.Errorf("%s: CleanName returned %v, want InvalidName", what, err)
			continue
		}
		// The message is shown to a guest word for word.
		if !strings.HasSuffix(invalid.Message, ".") {
			t.Errorf("%s: %q is not a sentence", what, invalid.Message)
		}
	}
}

// A name reaches the subject line of a notification email. A newline there is
// header injection, so the guarantee is worth asserting on its own rather than
// resting on the fact that CleanName happens to split on whitespace.
func TestACleanedNameIsAlwaysOneLine(t *testing.T) {
	messy := []string{
		"Maria\nSchmidt",
		"Maria\r\nBcc: everyone@example.org",
		"Maria\rSchmidt",
		"Maria\n\n\nSchmidt",
	}

	for _, in := range messy {
		got, err := store.CleanName(in)
		if err != nil {
			continue // refused outright is also a safe answer
		}
		if strings.ContainsAny(got, "\r\n") {
			t.Errorf("CleanName(%q) = %q, which still carries a line break", in, got)
		}
	}
}

func TestAFreshDatabaseHasThisYearsEvent(t *testing.T) {
	s := newStore(t)

	ev, err := s.Event(t.Context())
	if err != nil {
		t.Fatalf("reading the event: %v", err)
	}

	want := time.Date(2026, time.October, 17, 0, 0, 0, 0, time.UTC)
	if !ev.Date.Equal(want) {
		t.Errorf("date = %s, want %s", ev.Date, want)
	}
	if ev.Goal != 100 {
		t.Errorf("goal = %d, want 100", ev.Goal)
	}
	if got := ev.DateLine(); got != "Sat, Oct 17" {
		t.Errorf("DateLine = %q, want %q", got, "Sat, Oct 17")
	}
}

// Moving the date must not orphan the commitments already made. This is the
// reason the event id is not derived from the date.
func TestChangingTheDateKeepsTheCommitments(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	if _, err := s.Add(ctx, event, "Maria Schmidt", "b1"); err != nil {
		t.Fatalf("adding: %v", err)
	}

	ev, _ := s.Event(ctx)
	ev.Date = time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	if err := s.SaveEvent(ctx, ev); err != nil {
		t.Fatalf("moving the date: %v", err)
	}

	id, err := s.EventID(ctx)
	if err != nil {
		t.Fatalf("reading the event id: %v", err)
	}
	if id != event {
		t.Errorf("event id changed to %q when the date moved", id)
	}

	n, _ := s.Count(ctx, id)
	if n != 1 {
		t.Errorf("count = %d after moving the date, want 1", n)
	}
}

func TestAnEmptyInstructionIsRefused(t *testing.T) {
	s := newStore(t)
	ctx := t.Context()

	ev, _ := s.Event(ctx)
	ev.Instructions = "   "

	err := s.SaveEvent(ctx, ev)
	if _, ok := errors.AsType[store.InvalidEvent](err); !ok {
		t.Fatalf("SaveEvent returned %v, want InvalidEvent", err)
	}
}

// The list is typed by a person on a phone, so it has to survive the ways a
// person types a list. Anything else is a form that refuses an organiser who
// pasted three addresses out of an email.
func TestARecipientListSurvivesTheWayPeopleTypeIt(t *testing.T) {
	cases := map[string][]string{
		"a@x.org":                {"a@x.org"},
		"a@x.org,b@x.org":        {"a@x.org", "b@x.org"},
		" a@x.org , b@x.org ":    {"a@x.org", "b@x.org"},
		"a@x.org,,b@x.org,":      {"a@x.org", "b@x.org"},
		"a@x.org\nb@x.org":       {"a@x.org", "b@x.org"},
		"a@x.org\r\nb@x.org\r\n": {"a@x.org", "b@x.org"},
		"Maria <m@x.org>":        {"m@x.org"},
		"a@x.org, A@X.ORG":       {"a@x.org"},
		"":                       nil,
		"   ":                    nil,
		"\n\n":                   nil,
	}

	for in, want := range cases {
		got, err := store.ParseRecipients(in)
		if err != nil {
			t.Errorf("ParseRecipients(%q): %v", in, err)
			continue
		}
		if !slices.Equal(got, want) {
			t.Errorf("ParseRecipients(%q) = %v, want %v", in, got, want)
		}
	}
}

// The refusal has to name the entry at fault. "That is not valid" in front of
// five addresses is a puzzle, and this is being read in a hurry.
func TestAnAddressThatIsNotOneIsRefusedByName(t *testing.T) {
	_, err := store.ParseRecipients("a@x.org\nnot an address\nb@x.org")

	invalid, ok := errors.AsType[store.InvalidRecipients](err)
	if !ok {
		t.Fatalf("error = %v, want InvalidRecipients", err)
	}
	if !strings.Contains(invalid.Message, "not an address") {
		t.Errorf("the message does not name the entry at fault: %q", invalid.Message)
	}
}

func TestSavingTheListReplacesIt(t *testing.T) {
	s := newStore(t)

	if _, err := s.SaveNotifyRecipients(t.Context(), "a@x.org, b@x.org"); err != nil {
		t.Fatalf("saving: %v", err)
	}

	got, err := s.NotifyRecipients(t.Context())
	if err != nil {
		t.Fatalf("reading: %v", err)
	}
	if want := []string{"a@x.org", "b@x.org"}; !slices.Equal(got, want) {
		t.Fatalf("recipients = %v, want %v", got, want)
	}

	// Emptying it is a choice, not a failure: it means tell nobody.
	if _, err := s.SaveNotifyRecipients(t.Context(), ""); err != nil {
		t.Fatalf("emptying: %v", err)
	}
	if got, err := s.NotifyRecipients(t.Context()); err != nil || len(got) != 0 {
		t.Errorf("recipients = %v, %v; want empty", got, err)
	}
}

// The environment fills a database that has never had a list, and then stops
// mattering. Otherwise an address removed on the admin page would come back at
// the next deploy, which is a restart, and nobody would connect the two.
func TestTheEnvironmentSeedsTheListOnceAndThenStopsMattering(t *testing.T) {
	s := newStore(t)

	got, err := s.SeedNotifyRecipients(t.Context(), "a@x.org")
	if err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if want := []string{"a@x.org"}; !slices.Equal(got, want) {
		t.Fatalf("after seeding = %v, want %v", got, want)
	}

	if _, err := s.SaveNotifyRecipients(t.Context(), "b@x.org"); err != nil {
		t.Fatalf("saving over the seed: %v", err)
	}

	// The next start of the same binary with the same environment.
	got, err = s.SeedNotifyRecipients(t.Context(), "a@x.org")
	if err != nil {
		t.Fatalf("seeding again: %v", err)
	}
	if want := []string{"b@x.org"}; !slices.Equal(got, want) {
		t.Errorf("after a restart = %v, want %v -- the environment overwrote the admin page", got, want)
	}
}

// An empty environment must write nothing rather than write an empty list. A
// server started before the credential file is filled in would otherwise make
// NOTIFY_RECIPIENTS permanently irrelevant.
func TestAnEmptyEnvironmentLeavesTheListUnwritten(t *testing.T) {
	s := newStore(t)

	if _, err := s.SeedNotifyRecipients(t.Context(), ""); err != nil {
		t.Fatalf("seeding with nothing: %v", err)
	}

	got, err := s.SeedNotifyRecipients(t.Context(), "a@x.org")
	if err != nil {
		t.Fatalf("seeding after a blank start: %v", err)
	}
	if want := []string{"a@x.org"}; !slices.Equal(got, want) {
		t.Errorf("recipients = %v, want %v -- the blank start won", got, want)
	}
}
