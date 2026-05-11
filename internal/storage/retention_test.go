package storage

import (
	"context"
	"testing"
	"time"

	"meshmonday/internal/checkins"
	"meshmonday/internal/models"
)

func testRawPacket(packetHash string, observedAt time.Time) models.RawPacket {
	return models.RawPacket{
		PacketHash:      packetHash,
		Topic:           "meshcore/SEA/dev/packets",
		IATA:            "SEA",
		DevicePublicKey: "dev",
		PayloadHex:      "040041766572793a2054657374",
		PayloadType:     4,
		PayloadTypeName: "TXT_MSG",
		RouteType:       0,
		RouteTypeName:   "TRANSPORT_FLOOD",
		PathLen:         0,
		ObservedAt:      observedAt.UTC(),
		ReceivedAt:      observedAt.UTC(),
	}
}

func countRawPackets(t *testing.T, s *SQLiteStore) int {
	t.Helper()
	var n int
	if err := s.db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM raw_packets`).Scan(&n); err != nil {
		t.Fatalf("count raw_packets: %v", err)
	}
	return n
}

func TestPruneRawPacketsDeletesOffMondayAndOldMondays(t *testing.T) {
	dbPath := t.TempDir() + "/retention.db"
	store, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()

	tz := "America/Los_Angeles"
	// Monday May 4, 2026 (within 1 week of May 11) — ambient Monday traffic kept.
	monRecent := time.Date(2026, 5, 4, 16, 0, 0, 0, time.UTC)
	// Monday April 27, 2026 — before cutoff (May 4) when retainWeeks=1 from May 11.
	monOld := time.Date(2026, 4, 27, 16, 0, 0, 0, time.UTC)
	// Tuesday May 5, 2026 — not Monday.
	tuesday := time.Date(2026, 5, 5, 15, 0, 0, 0, time.UTC)

	if !checkins.IsMondayInTZ(monRecent, tz) || !checkins.IsMondayInTZ(monOld, tz) {
		t.Fatal("fixture dates must be Monday in TZ")
	}
	if checkins.IsMondayInTZ(tuesday, tz) {
		t.Fatal("fixture tuesday must not be Monday in TZ")
	}

	_, _ = store.InsertRawPacket(context.Background(), testRawPacket("hash_mon_recent", monRecent))
	_, _ = store.InsertRawPacket(context.Background(), testRawPacket("hash_mon_old", monOld))
	_, _ = store.InsertRawPacket(context.Background(), testRawPacket("hash_tuesday", tuesday))

	if count := countRawPackets(t, store); count != 3 {
		t.Fatalf("expected 3 raw rows before prune, got %d", count)
	}

	now := time.Date(2026, 5, 11, 18, 0, 0, 0, time.UTC)
	deleted, err := store.PruneRawPackets(context.Background(), now, tz, 1)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted, got %d", deleted)
	}
	if count := countRawPackets(t, store); count != 1 {
		t.Fatalf("expected 1 raw row after prune, got %d", count)
	}
}

func TestPruneRawPacketsKeepsCheckinLinkedOffMonday(t *testing.T) {
	dbPath := t.TempDir() + "/retention_checkin.db"
	store, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()

	tz := "America/Los_Angeles"
	tuesday := time.Date(2026, 5, 5, 15, 0, 0, 0, time.UTC)
	p := testRawPacket("hash_checkin_tue", tuesday)
	if _, err := store.InsertRawPacket(context.Background(), p); err != nil {
		t.Fatalf("insert raw: %v", err)
	}
	weekStart := checkins.WeekStartMonday(tuesday, tz)
	if _, err := store.InsertCheckin(context.Background(), models.Checkin{
		PacketHash:  p.PacketHash,
		Username:    "testuser",
		DisplayName: "Test User",
		Message:     "hello #meshmonday",
		IATA:        "SEA",
		CheckinDate: tuesday,
		WeekStart:   weekStart,
		CreatedAt:   time.Now().UTC(),
	}); err != nil {
		t.Fatalf("insert checkin: %v", err)
	}

	now := time.Date(2026, 5, 11, 18, 0, 0, 0, time.UTC)
	deleted, err := store.PruneRawPackets(context.Background(), now, tz, 1)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted (checkin-linked), got %d", deleted)
	}
	if count := countRawPackets(t, store); count != 1 {
		t.Fatalf("expected 1 raw row, got %d", count)
	}
}

func TestPruneRawPacketsRetainWeeksZeroDropsAmbientMonday(t *testing.T) {
	dbPath := t.TempDir() + "/retention_w0.db"
	store, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer store.Close()

	tz := "America/Los_Angeles"
	monRecent := time.Date(2026, 5, 4, 16, 0, 0, 0, time.UTC)
	_, _ = store.InsertRawPacket(context.Background(), testRawPacket("hash_only_mon", monRecent))

	now := time.Date(2026, 5, 11, 18, 0, 0, 0, time.UTC)
	deleted, err := store.PruneRawPackets(context.Background(), now, tz, 0)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}
	if count := countRawPackets(t, store); count != 0 {
		t.Fatalf("expected 0 raw rows, got %d", count)
	}
}
