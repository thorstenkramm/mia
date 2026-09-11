package sqlite

import (
	"database/sql"
	"errors"
)

// ScanStrings collects a single TEXT column and closes rows on every path,
// including the scan and iteration error paths. The returned error keeps the
// underlying scan, iteration, or close error identity so callers can add their
// own context. Multi-column scans keep their own row handling because they also
// own per-row decoding and validation.
func ScanStrings(rows *sql.Rows) ([]string, error) {
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Join(err, rows.Close())
	}
	return values, rows.Close()
}
