package store

import (
	"testing"
	"time"
)

func TestFormatTimeIsUTCWithSecondPrecision(t *testing.T) {
	brt := time.FixedZone("BRT", -3*60*60)
	got := FormatTime(time.Date(2026, 11, 12, 13, 0, 0, 999_000_000, brt))
	if want := "2026-11-12T16:00:00Z"; got != want {
		t.Errorf("FormatTime = %q, want %q", got, want)
	}
}

func TestParseTimeRoundTrip(t *testing.T) {
	want := time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)
	got, err := ParseTime(FormatTime(want))
	if err != nil {
		t.Fatalf("ParseTime: %v", err)
	}
	if !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("ParseTime = %v (%v), want %v in UTC", got, got.Location(), want)
	}
}

func TestParseTimeConvertsOffsetsToUTC(t *testing.T) {
	got, err := ParseTime("2026-11-12T13:00:00-03:00")
	if err != nil {
		t.Fatalf("ParseTime: %v", err)
	}
	if want := time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC); !got.Equal(want) || got.Location() != time.UTC {
		t.Errorf("ParseTime = %v (%v), want %v in UTC", got, got.Location(), want)
	}
}

func TestParseTimeRejectsOtherLayouts(t *testing.T) {
	for _, s := range []string{"", "2026-11-12", "2026-11-12 16:00:00", "12/11/2026", "not a time"} {
		if _, err := ParseTime(s); err == nil {
			t.Errorf("ParseTime(%q) succeeded, want an error", s)
		}
	}
}
