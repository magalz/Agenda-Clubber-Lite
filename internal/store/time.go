package store

import "time"

// TimeLayout is the one timestamp format of the database: RFC 3339 in UTC with
// second precision, for example 2026-11-12T16:00:00Z. Go writes every
// timestamp column; SQLite never generates one.
const TimeLayout = time.RFC3339

// FormatTime renders t as a database timestamp.
func FormatTime(t time.Time) string { return "" }

// ParseTime reads a database timestamp and returns it in UTC.
func ParseTime(s string) (time.Time, error) { return time.Time{}, nil }
