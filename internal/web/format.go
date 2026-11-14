package web

import "time"

// Stubs: the tests are written first and must fail on their assertions.

func fmtDate(t time.Time, loc *time.Location) string      { return "" }
func fmtDateLong(t time.Time, loc *time.Location) string  { return "" }
func fmtMonthYear(t time.Time, loc *time.Location) string { return "" }
func fmtClock(t time.Time, loc *time.Location) string     { return "" }

func fmtUntil(start time.Time, durationMin int, loc *time.Location) string { return "" }

func fmtDuration(minutes int) string { return "" }

func fmtPrice(free bool, cents int64) string { return "" }
