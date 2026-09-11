package web_test

import (
	"io"
	"log/slog"
)

// discardLogger keeps the test output readable. A handler that logs an error
// is not necessarily a test that failed -- several of these exercise exactly
// the paths that log.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
