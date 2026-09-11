package mail

import (
	"fmt"
	"strings"
	"time"
)

// The notifications deliberately echo last year's, which people already
// recognise: a line naming what happened, the name, the event, the time, and
// the running count. The count is the part organisers actually read -- it
// answers "are we going to reach a hundred" without opening anything.

// Committed is the notification sent when somebody promises flowers.
func Committed(name, event string, when time.Time, total int) Message {
	return Message{
		Subject: fmt.Sprintf("Flower Commitment - %s", name),
		Body: body("🌸 New Flower Commitment:", []string{
			"Name: " + name,
			"Event: " + event,
			"Committed: " + stamp(when),
			fmt.Sprintf("Total commitments: %d", total),
		}),
	}
}

// Cancelled is the notification sent when somebody withdraws.
func Cancelled(name, event string, when time.Time, total int) Message {
	return Message{
		Subject: fmt.Sprintf("Flower Commitment Canceled - %s", name),
		Body: body("🌸 Flower Commitment Canceled:", []string{
			"Name: " + name,
			"Event: " + event,
			"Canceled: " + stamp(when),
			fmt.Sprintf("Remaining commitments: %d", total),
		}),
	}
}

// SignIn is the magic link. It goes to one organiser and nobody else.
func SignIn(link string, validFor time.Duration) Message {
	return Message{
		Subject: "Sign in to the flowers page",
		Body: strings.Join([]string{
			"Open this link to sign in:",
			"",
			link,
			"",
			fmt.Sprintf("It works once and stops working in %d minutes.", int(validFor.Minutes())),
			"",
			"If you did not ask to sign in, nothing has happened and you can ignore this.",
			"",
		}, "\n"),
	}
}

func body(heading string, lines []string) string {
	var b strings.Builder

	b.WriteString(heading)
	b.WriteString("\n\n")
	for _, l := range lines {
		b.WriteString("• ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	b.WriteString("\nThis is an automated notification from the church flowers system.\n")

	return b.String()
}

// stamp writes the time the way the organisers read it, in the parish's own
// zone rather than UTC. A notification saying 03:06 for something that
// happened at ten in the evening is a small thing that makes a system feel
// broken.
func stamp(t time.Time) string {
	return t.In(parishTime()).Format("01-02-2006 03:04 PM MST")
}

// parishTime is US Central, where the Shrine is. It falls back to UTC on a
// machine with no zone database rather than failing -- a wrong-looking
// timestamp is better than no notification.
func parishTime() *time.Location {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		return time.UTC
	}
	return loc
}
