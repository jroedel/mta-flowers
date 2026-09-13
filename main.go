// Command mta-flowers collects commitments to bring flowers to the Shrine for
// the Feast of Our Lady of Schoenstatt.
//
// It is one process: an HTTP server, a SQLite file, and an SMTP client. It
// expects to run behind Caddy, which holds the certificate and forwards to
// loopback. See docs/scope.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jroedel/mta-flowers/internal/mail"
	"github.com/jroedel/mta-flowers/internal/store"
	"github.com/jroedel/mta-flowers/internal/web"
)

func main() {
	if err := run(); err != nil {
		// slog rather than log.Fatal so a startup failure is shaped like every
		// other line in the journal, which is where somebody will read it.
		slog.Error("mta-flowers could not start", "error", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg.databasePath)
	if err != nil {
		return err
	}
	defer st.Close()

	// Who is told is a setting, not configuration: it is edited on the admin
	// page, and NOTIFY_RECIPIENTS only fills a database that has never had a
	// list. See store.SeedNotifyRecipients for why the environment stops
	// mattering the moment an organiser saves one.
	notify, err := st.SeedNotifyRecipients(ctx, cfg.notifyRecipients)
	if err != nil {
		// Not fatal. A mistyped credential file must not keep the button from
		// working, and whatever is already saved on the admin page still
		// stands -- but nothing else in the system would ever mention this,
		// so it is a warning that names the value.
		log.Warn("NOTIFY_RECIPIENTS could not be read and was ignored",
			"value", cfg.notifyRecipients, "error", err)

		if notify, err = st.NotifyRecipients(ctx); err != nil {
			return err
		}
	}
	if len(notify) == 0 {
		// The same invisible failure as an unconfigured Sender: everything
		// works and nobody hears about it.
		log.Warn("nobody is on the notification list, so no one will be told when somebody commits",
			"fix", "add addresses under \"Who is told\" on the admin page")
	}

	sender := mail.Sender{
		Host:     cfg.smtpHost,
		Port:     cfg.smtpPort,
		Username: cfg.smtpUsername,
		Password: cfg.smtpPassword,
		From:     cfg.mailFrom,
		Log:      log,
	}

	if !sender.Configured() {
		// Loud, because this is the failure that is invisible from the
		// outside: the button works, the count goes up, and nobody is told.
		log.Warn("mail is not configured: no notifications and no sign-in links will be sent",
			"want", "SMTP_HOST, SMTP_USERNAME, SMTP_PASSWORD and MAIL_FROM")
	}

	srv, err := web.New(st, sender, cfg.web, log)
	if err != nil {
		return fmt.Errorf("building the server: %w", err)
	}

	httpServer := &http.Server{
		Addr:    cfg.listen,
		Handler: srv.Handler(),

		// Caddy is the only client, but these are what stop a stuck connection
		// from holding a goroutine for the life of the process.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go purgeExpiredPeriodically(ctx, st, log)

	errs := make(chan error, 1)
	go func() {
		log.Info("listening",
			"addr", cfg.listen,
			"public", cfg.web.PublicURL,
			"database", cfg.databasePath,
			"admins", len(cfg.web.AdminEmails),
			"notify", len(notify))

		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- fmt.Errorf("listening on %s: %w", cfg.listen, err)
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	// Long enough to finish the request in flight, short enough that systemd
	// does not lose patience and send SIGKILL during a deploy.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutting down: %w", err)
	}

	log.Info("stopped")
	return nil
}

// purgeExpiredPeriodically clears spent sign-in links and dead sessions.
// Nothing depends on it: every read checks expiry itself, so a failure here is
// housekeeping left undone and not a way in.
func purgeExpiredPeriodically(ctx context.Context, st *store.Store, log *slog.Logger) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := st.PurgeExpired(ctx); err != nil {
				log.Error("clearing expired sign-in links and sessions", "error", err)
			}
		}
	}
}

type config struct {
	listen       string
	databasePath string
	smtpHost     string
	smtpPort     string
	smtpUsername string
	smtpPassword string
	mailFrom     string

	// notifyRecipients is only a seed for the first start of a fresh
	// database, which is why it is here and not in web.Config: no handler
	// reads it.
	notifyRecipients string

	web web.Config
}

// loadConfig reads the environment, which on the server is
// /etc/mta-flowers.env loaded by systemd. Every name matches the one in
// ~/.config/mta-flowers.env, so the file you keep and the file the server
// reads are the same shape.
func loadConfig() (config, error) {
	publicURL := strings.TrimRight(env("PUBLIC_URL", "http://localhost:8080"), "/")

	cfg := config{
		listen:       env("LISTEN_ADDR", "127.0.0.1:8080"),
		databasePath: env("DATABASE_PATH", "flowers.db"),
		smtpHost:     env("SMTP_HOST", ""),
		smtpPort:     env("SMTP_PORT", "587"),
		smtpUsername: env("SMTP_USERNAME", ""),
		smtpPassword: env("SMTP_PASSWORD", ""),
		mailFrom:     env("MAIL_FROM", ""),

		notifyRecipients: env("NOTIFY_RECIPIENTS", ""),

		web: web.Config{
			PublicURL:        publicURL,
			AllowedOrigins:   splitList(env("ALLOWED_ORIGINS", "https://schoenstatt-austin.us,https://www.schoenstatt-austin.us")),
			AdminEmails:      splitList(env("ADMIN_EMAILS", "")),
			FallbackPassword: env("ADMIN_FALLBACK_PASSWORD", ""),
			EventName:        env("EVENT_NAME", "Feast Day Celebration"),
		},
	}

	// One refusal rather than a warning. An organiser who cannot sign in
	// cannot fix anything, and the failure would otherwise appear as a
	// sign-in page that quietly never sends a link.
	if len(cfg.web.AdminEmails) == 0 {
		return config{}, errors.New(
			"ADMIN_EMAILS is empty, so nobody could sign in to the admin pages")
	}

	return cfg, nil
}

func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func splitList(raw string) []string {
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}
