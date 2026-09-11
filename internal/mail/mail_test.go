package mail_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jroedel/mta-flowers/internal/mail"
)

func TestRecipientsTolerateTheWayPeopleTypeLists(t *testing.T) {
	cases := map[string][]string{
		"a@x.org":             {"a@x.org"},
		"a@x.org,b@x.org":     {"a@x.org", "b@x.org"},
		" a@x.org , b@x.org ": {"a@x.org", "b@x.org"},
		"a@x.org,,b@x.org,":   {"a@x.org", "b@x.org"},
		"":                    nil,
		"   ":                 nil,
	}

	for in, want := range cases {
		got := mail.Recipients(in)
		if len(got) != len(want) {
			t.Errorf("Recipients(%q) = %v, want %v", in, got, want)
			continue
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("Recipients(%q) = %v, want %v", in, got, want)
				break
			}
		}
	}
}

// The notification names the person and carries the running total, because the
// total is the number organisers are actually watching.
func TestTheNotificationSaysWhatHappenedAndHowManySoFar(t *testing.T) {
	when := time.Date(2026, time.September, 12, 9, 6, 0, 0, time.UTC)
	m := mail.Committed("Fr. Jeff Roedel", "Feast Day Celebration", when, 42)

	if !strings.Contains(m.Subject, "Fr. Jeff Roedel") {
		t.Errorf("subject %q does not name the person", m.Subject)
	}
	for _, want := range []string{"Fr. Jeff Roedel", "Feast Day Celebration", "Total commitments: 42"} {
		if !strings.Contains(m.Body, want) {
			t.Errorf("body is missing %q:\n%s", want, m.Body)
		}
	}
}

func TestTheCancellationSaysWhatIsLeft(t *testing.T) {
	m := mail.Cancelled("Fr. Jeff Roedel", "Feast Day Celebration", time.Now(), 0)

	if !strings.Contains(m.Subject, "Canceled") {
		t.Errorf("subject %q does not say it is a cancellation", m.Subject)
	}
	if !strings.Contains(m.Body, "Remaining commitments: 0") {
		t.Errorf("body does not carry the remaining count:\n%s", m.Body)
	}
}

// A magic link that is quoted, wrapped or rewritten is a link that does not
// work. It must appear on a line of its own.
func TestTheSignInLinkIsOnItsOwnLine(t *testing.T) {
	link := "https://flowers.schoenstatt.link/admin/enter?token=abc123"
	m := mail.SignIn(link, 15*time.Minute)

	var found bool
	for line := range strings.SplitSeq(m.Body, "\n") {
		if strings.TrimSpace(line) == link {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("the link is not on a line of its own:\n%s", m.Body)
	}
	if !strings.Contains(m.Body, "15 minutes") {
		t.Errorf("the message does not say how long it lasts:\n%s", m.Body)
	}
}

// An unconfigured Sender must say so distinctly, because a development machine
// has no password and that is not a failure.
func TestAnUnconfiguredSenderIsRecognisable(t *testing.T) {
	var s mail.Sender
	if s.Configured() {
		t.Error("the zero Sender claims to be configured")
	}

	err := s.Send(t.Context(), mail.Message{To: []string{"a@x.org"}, Subject: "x"})
	if err == nil {
		t.Fatal("sending with no configuration reported success")
	}
	if err.Error() != "mail is not configured" {
		t.Errorf("error = %q, want the not-configured sentinel", err)
	}
}
