// Package mail sends the two kinds of message this program produces: a
// notification when somebody commits or cancels, and a sign-in link for an
// organiser.
//
// It submits to Hetzner rather than delivering directly, and that is the whole
// reason this package is not six lines of net/smtp. Mail sent straight from
// the Vultr instance is covered by neither our SPF record nor our DKIM key,
// and schoenstatt.link publishes p=quarantine -- so it would be filed as spam
// or dropped, and nobody would learn that anybody had signed up. Submitting
// with SMTP AUTH relays the message through our own server, which signs it and
// delivers it from the address SPF authorises.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Sender holds the submission settings. The zero value does not send; see
// Disabled, which is what runs on a development machine.
type Sender struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string

	// Log receives every send, successful or not. A notification that failed
	// must leave a trace somewhere, because the guest is never told.
	Log *slog.Logger
}

// Message is one email. Body is plain text: these go to a handful of people
// who want to know a name and a number, and HTML would add a second thing to
// get right in every mail client for no gain.
type Message struct {
	To      []string
	Subject string
	Body    string
}

// ErrNotConfigured is returned when a send is attempted with no password set.
// It is expected on a development machine and must not be treated as a
// failure of the thing that triggered it.
var ErrNotConfigured = errors.New("mail is not configured")

// Configured reports whether this Sender can actually send.
func (s Sender) Configured() bool {
	return s.Host != "" && s.Username != "" && s.Password != "" && s.From != ""
}

// Send delivers one message, synchronously. Callers who are answering a guest
// should use SendAsync instead.
func (s Sender) Send(ctx context.Context, m Message) error {
	if !s.Configured() {
		return ErrNotConfigured
	}
	if len(m.To) == 0 {
		return errors.New("nobody to send to")
	}

	addr := net.JoinHostPort(s.Host, cmpOr(s.Port, "587"))

	// A deadline rather than a bare Dial: submission to a host that is
	// accepting connections but not answering would otherwise hold this
	// goroutine until the process restarts.
	d := net.Dialer{Timeout: 15 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("reaching the mail server at %s: %w", addr, err)
	}

	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("starting the conversation with %s: %w", addr, err)
	}
	defer c.Close()

	// STARTTLS on 587. Refusing to continue without it is deliberate: the
	// password below is the only thing protecting a mailbox that can send as
	// schoenstatt.link, and an attacker who can strip STARTTLS can read it.
	if ok, _ := c.Extension("STARTTLS"); !ok {
		return fmt.Errorf("%s will not start TLS, so the password cannot be sent safely", addr)
	}
	if err := c.StartTLS(&tls.Config{ServerName: s.Host}); err != nil {
		return fmt.Errorf("starting TLS with %s: %w", addr, err)
	}

	if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
		return fmt.Errorf("signing in to %s as %s: %w", addr, s.Username, err)
	}

	if err := c.Mail(s.From); err != nil {
		return fmt.Errorf("setting the sender: %w", err)
	}
	for _, to := range m.To {
		if err := c.Rcpt(to); err != nil {
			return fmt.Errorf("setting the recipient %s: %w", to, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("starting the message body: %w", err)
	}
	if _, err := w.Write([]byte(s.compose(m))); err != nil {
		return fmt.Errorf("writing the message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finishing the message: %w", err)
	}

	return c.Quit()
}

// SendAsync sends in the background and logs anything that goes wrong.
//
// Every caller in this program is answering a browser. A guest pressing the
// button must not see an error because Hetzner is slow, and an organiser must
// not wait on an SMTP round trip; the commitment is already recorded by the
// time this is called, and the database is the record of truth. What a failure
// costs is that nobody is told, which is what the log is for.
func (s Sender) SendAsync(m Message) {
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 60*time.Second)
		defer cancel()

		if err := s.Send(ctx, m); err != nil {
			if errors.Is(err, ErrNotConfigured) {
				s.logger().Info("mail is not configured, so nothing was sent",
					"subject", m.Subject, "recipients", len(m.To))
				return
			}
			s.logger().Error("sending mail failed; nobody was told",
				"subject", m.Subject, "recipients", len(m.To), "error", err)
			return
		}

		s.logger().Info("sent", "subject", m.Subject, "recipients", len(m.To))
	}()
}

// compose builds the RFC 5322 message.
//
// Every header value here is either ours or has been through store.CleanName,
// which collapses whitespace -- so a newline cannot reach a header and inject
// one of its own. That guarantee is asserted in the store's tests rather than
// re-checked here, but it is the reason this function can use plain
// concatenation.
func (s Sender) compose(m Message) string {
	var b strings.Builder

	fmt.Fprintf(&b, "From: %s\r\n", s.From)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(m.To, ", "))
	// Encoded because a name can carry an accent, and a raw non-ASCII byte in
	// a Subject is not legal and renders as mojibake where it is tolerated.
	fmt.Fprintf(&b, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", m.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")

	// Bare newlines from a Go string literal are not legal line endings in
	// SMTP, and some servers accept them while others do not.
	b.WriteString(strings.ReplaceAll(m.Body, "\n", "\r\n"))

	return b.String()
}

func (s Sender) logger() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Recipients splits a comma-separated list from the environment, dropping
// blanks and spaces. An empty list is a legitimate answer: it means nobody
// asked to be told.
func Recipients(raw string) []string {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		if addr := strings.TrimSpace(part); addr != "" {
			out = append(out, addr)
		}
	}
	return out
}
