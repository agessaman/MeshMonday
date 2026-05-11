package checkins

import (
	"testing"
	"time"
)

func TestCountMeshMondayWeeksInclusive(t *testing.T) {
	tz := "America/Los_Angeles"
	// Jan 5, 2026 is a Monday (week A). Jan 12, 2026 is the next Monday (week B).
	from := time.Date(2026, 1, 5, 15, 0, 0, 0, time.UTC)
	through := time.Date(2026, 1, 12, 10, 0, 0, 0, time.UTC)
	if n := CountMeshMondayWeeksInclusive(from, through, tz); n != 2 {
		t.Fatalf("expected 2 inclusive weeks, got %d", n)
	}
	// Same week → one bucket
	same := time.Date(2026, 1, 7, 12, 0, 0, 0, time.UTC)
	if n := CountMeshMondayWeeksInclusive(from, same, tz); n != 1 {
		t.Fatalf("expected 1 week, got %d", n)
	}
	// through before track window
	early := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	if n := CountMeshMondayWeeksInclusive(from, early, tz); n != 0 {
		t.Fatalf("expected 0 weeks, got %d", n)
	}
}
