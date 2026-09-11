package store

import (
	"context"
	"fmt"
)

// DebugColumn reads one column of one table. It exists for a test that asserts
// no live token is ever written to disk, which cannot be checked through the
// ordinary API precisely because the ordinary API never hands a stored token
// back.
//
// The table and column are interpolated rather than bound, because SQLite
// cannot parameterise an identifier. That is safe here and nowhere else: both
// arguments come from test code, never from a request. Do not call this from a
// handler.
func (s *Store) DebugColumn(ctx context.Context, table, column string) ([]string, error) {
	switch table {
	case "admin_links", "admin_sessions", "commitments", "settings":
	default:
		return nil, fmt.Errorf("DebugColumn refuses the table %q", table)
	}

	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("SELECT %s FROM %s", column, table)) //nolint:gosec // see above
	if err != nil {
		return nil, fmt.Errorf("reading %s.%s: %w", table, column, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("reading %s.%s: %w", table, column, err)
		}
		out = append(out, v)
	}

	return out, rows.Err()
}
