package storage

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"meshmonday/internal/models"
)

type SQLiteStore struct {
	db *sql.DB
}

type RawPacketRestoreRow struct {
	PacketHash string
	PayloadHex string
	ObservedAt time.Time
}

// sqliteDSN sets PRAGMAs on every pooled connection (modernc.org/sqlite).
// A single db.Exec(PRAGMA ...) after Open only affects one connection, which
// caused SQLITE_BUSY during concurrent retention deletes.
func sqliteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout=30000")
	q.Add("_pragma", "journal_mode=WAL")
	q.Add("_pragma", "foreign_keys=ON")
	return path + "?" + q.Encode()
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create sqlite dir: %w", err)
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	store := &SQLiteStore{db: db}
	if err := store.Migrate(context.Background()); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

func (s *SQLiteStore) Migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS raw_packets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  packet_hash TEXT NOT NULL UNIQUE,
  topic TEXT NOT NULL,
  iata TEXT NOT NULL,
  device_public_key TEXT NOT NULL,
  payload_hex TEXT NOT NULL,
  payload_type INTEGER NOT NULL,
  payload_type_name TEXT NOT NULL,
  route_type INTEGER NOT NULL,
  route_type_name TEXT NOT NULL,
  path_len INTEGER NOT NULL DEFAULT 0,
  observed_at TEXT NOT NULL,
  received_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS checkins (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  packet_hash TEXT NOT NULL UNIQUE,
  username TEXT NOT NULL,
  display_name TEXT NOT NULL,
  message TEXT NOT NULL,
  iata TEXT NOT NULL,
  checkin_date TEXT NOT NULL,
  week_start TEXT NOT NULL,
  created_at TEXT NOT NULL,
  FOREIGN KEY(packet_hash) REFERENCES raw_packets(packet_hash) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS packet_observations (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  packet_hash TEXT NOT NULL,
  observer_key TEXT NOT NULL,
  observed_at TEXT NOT NULL,
  UNIQUE(packet_hash, observer_key),
  FOREIGN KEY(packet_hash) REFERENCES raw_packets(packet_hash) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS checkin_packets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  week_start TEXT NOT NULL,
  username TEXT NOT NULL,
  packet_hash TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(week_start, username, packet_hash),
  FOREIGN KEY(packet_hash) REFERENCES raw_packets(packet_hash) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS leaderboard_snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  metric_type TEXT NOT NULL,
  username TEXT NOT NULL,
  display_name TEXT NOT NULL,
  metric_value INTEGER NOT NULL,
  streak_start TEXT,
  tracked_from TEXT NOT NULL,
  computed_at TEXT NOT NULL
);

-- Backfill cleanup before enforcing stricter uniqueness rules:
-- one check-in per user per week (across all IATA regions).
DELETE FROM checkins
WHERE id NOT IN (
  SELECT MIN(id)
  FROM checkins
  GROUP BY week_start, username
);

CREATE INDEX IF NOT EXISTS idx_checkins_iata_checkin_date ON checkins (iata, checkin_date);
CREATE INDEX IF NOT EXISTS idx_checkins_username_checkin_date ON checkins (username, checkin_date);
DROP INDEX IF EXISTS idx_checkins_iata_week_username_unique;
CREATE UNIQUE INDEX IF NOT EXISTS idx_checkins_week_username_unique ON checkins (week_start, username);
CREATE INDEX IF NOT EXISTS idx_snapshots_metric_computed ON leaderboard_snapshots (metric_type, computed_at);
CREATE INDEX IF NOT EXISTS idx_packet_observations_packet_hash ON packet_observations (packet_hash);
CREATE INDEX IF NOT EXISTS idx_checkin_packets_week_user ON checkin_packets (week_start, username);
CREATE INDEX IF NOT EXISTS idx_checkin_packets_packet_hash ON checkin_packets (packet_hash);
CREATE INDEX IF NOT EXISTS idx_raw_packets_observed_at ON raw_packets (observed_at);

-- Backfill link table from existing canonical checkins.
INSERT OR IGNORE INTO checkin_packets (week_start, username, packet_hash, created_at)
SELECT week_start, username, packet_hash, created_at
FROM checkins;
`
	_, err := s.db.ExecContext(ctx, schema)
	return err
}

func (s *SQLiteStore) InsertRawPacket(ctx context.Context, packet models.RawPacket) (bool, error) {
	const q = `
INSERT INTO raw_packets (
	packet_hash, topic, iata, device_public_key, payload_hex, payload_type, payload_type_name,
	route_type, route_type_name, path_len, observed_at, received_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
`
	_, err := s.db.ExecContext(ctx, q,
		packet.PacketHash,
		packet.Topic,
		packet.IATA,
		packet.DevicePublicKey,
		packet.PayloadHex,
		packet.PayloadType,
		packet.PayloadTypeName,
		packet.RouteType,
		packet.RouteTypeName,
		packet.PathLen,
		packet.ObservedAt.Format(time.RFC3339),
		packet.ReceivedAt.Format(time.RFC3339),
	)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) InsertCheckin(ctx context.Context, checkin models.Checkin) (bool, error) {
	const q = `
INSERT INTO checkins (
	packet_hash, username, display_name, message, iata, checkin_date, week_start, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT DO NOTHING;
`
	result, err := s.db.ExecContext(ctx, q,
		checkin.PacketHash,
		checkin.Username,
		checkin.DisplayName,
		checkin.Message,
		checkin.IATA,
		checkin.CheckinDate.Format("2006-01-02"),
		checkin.WeekStart.Format("2006-01-02"),
		checkin.CreatedAt.Format(time.RFC3339),
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

func (s *SQLiteStore) InsertPacketObservation(ctx context.Context, packetHash, observerKey string, observedAt time.Time) (bool, error) {
	const q = `
INSERT INTO packet_observations (packet_hash, observer_key, observed_at)
VALUES (?, ?, ?);
`
	_, err := s.db.ExecContext(ctx, q, packetHash, observerKey, observedAt.Format(time.RFC3339))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) InsertCheckinPacket(ctx context.Context, weekStart time.Time, username, packetHash string, createdAt time.Time) (bool, error) {
	const q = `
INSERT INTO checkin_packets (week_start, username, packet_hash, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT DO NOTHING;
`
	result, err := s.db.ExecContext(ctx, q,
		weekStart.Format("2006-01-02"),
		username,
		packetHash,
		createdAt.Format(time.RFC3339),
	)
	if err != nil {
		return false, err
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rowsAffected > 0, nil
}

func (s *SQLiteStore) ListCheckinsByWeek(ctx context.Context, iataFilters []string, weekStart time.Time) ([]models.Checkin, error) {
	base := `
SELECT
	c.id,
	c.packet_hash,
	c.username,
	c.display_name,
	c.message,
	c.iata,
	c.checkin_date,
	c.week_start,
	c.created_at,
	COALESCE(COUNT(DISTINCT po.observer_key), 0) AS observer_count,
	COALESCE(GROUP_CONCAT(DISTINCT po.observer_key), '') AS observer_names
FROM checkins
AS c
LEFT JOIN checkin_packets AS cp
	ON cp.week_start = c.week_start AND cp.username = c.username
LEFT JOIN packet_observations AS po
	ON po.packet_hash = cp.packet_hash
WHERE c.week_start = ?
`
	args := []any{weekStart.Format("2006-01-02")}
	if len(iataFilters) > 0 {
		placeholders := make([]string, 0, len(iataFilters))
		for _, iata := range iataFilters {
			placeholders = append(placeholders, "?")
			args = append(args, iata)
		}
		base += `
 AND EXISTS (
   SELECT 1
   FROM checkin_packets AS cp_filter
   JOIN raw_packets AS rp_filter
     ON rp_filter.packet_hash = cp_filter.packet_hash
   WHERE cp_filter.week_start = c.week_start
     AND cp_filter.username = c.username
     AND rp_filter.iata IN (` + strings.Join(placeholders, ", ") + `)
 )
`
	}
	q := base + `
GROUP BY c.id, c.packet_hash, c.username, c.display_name, c.message, c.iata, c.checkin_date, c.week_start, c.created_at
ORDER BY c.created_at DESC, c.display_name ASC;
`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []models.Checkin
	for rows.Next() {
		var row models.Checkin
		var checkinDate string
		var weekStartRaw string
		var createdAt string
		var observerNames string
		if err := rows.Scan(
			&row.ID,
			&row.PacketHash,
			&row.Username,
			&row.DisplayName,
			&row.Message,
			&row.IATA,
			&checkinDate,
			&weekStartRaw,
			&createdAt,
			&row.ObserverCount,
			&observerNames,
		); err != nil {
			return nil, err
		}
		row.CheckinDate, _ = time.Parse("2006-01-02", checkinDate)
		row.WeekStart, _ = time.Parse("2006-01-02", weekStartRaw)
		row.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		row.ObserverNames = parseObserverNames(observerNames)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *SQLiteStore) ListCheckinsSince(ctx context.Context, iataFilters []string, trackFrom time.Time) ([]models.Checkin, error) {
	base := `
SELECT id, packet_hash, username, display_name, message, iata, checkin_date, week_start, created_at
FROM checkins
WHERE checkin_date >= ?
`
	args := []any{trackFrom.Format("2006-01-02")}
	if len(iataFilters) > 0 {
		placeholders := make([]string, 0, len(iataFilters))
		for _, iata := range iataFilters {
			placeholders = append(placeholders, "?")
			args = append(args, iata)
		}
		base += `
 AND EXISTS (
   SELECT 1
   FROM checkin_packets AS cp_filter
   JOIN raw_packets AS rp_filter
     ON rp_filter.packet_hash = cp_filter.packet_hash
   WHERE cp_filter.week_start = checkins.week_start
     AND cp_filter.username = checkins.username
     AND rp_filter.iata IN (` + strings.Join(placeholders, ", ") + `)
 )
`
	}
	q := base + `
ORDER BY checkin_date ASC, username ASC;
`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []models.Checkin
	for rows.Next() {
		var row models.Checkin
		var checkinDate string
		var weekStartRaw string
		var createdAt string
		if err := rows.Scan(
			&row.ID,
			&row.PacketHash,
			&row.Username,
			&row.DisplayName,
			&row.Message,
			&row.IATA,
			&checkinDate,
			&weekStartRaw,
			&createdAt,
		); err != nil {
			return nil, err
		}
		row.CheckinDate, _ = time.Parse("2006-01-02", checkinDate)
		row.WeekStart, _ = time.Parse("2006-01-02", weekStartRaw)
		row.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *SQLiteStore) ReplaceLeaderboardSnapshots(ctx context.Context, trackedFrom time.Time, entries []models.LeaderboardEntry, computedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, "DELETE FROM leaderboard_snapshots;"); err != nil {
		return err
	}

	const q = `
INSERT INTO leaderboard_snapshots (
	metric_type, username, display_name, metric_value, streak_start, tracked_from, computed_at
) VALUES (?, ?, ?, ?, ?, ?, ?);
`
	for _, entry := range entries {
		if _, err := tx.ExecContext(ctx, q, "most_checkins", entry.Username, entry.DisplayName, entry.MostCheckins, nil, entry.TrackedFrom, computedAt.Format(time.RFC3339)); err != nil {
			return err
		}
		var streakStart any
		if entry.StreakStartDate != "" {
			streakStart = entry.StreakStartDate
		}
		if _, err := tx.ExecContext(ctx, q, "longest_streak", entry.Username, entry.DisplayName, entry.LongestStreak, streakStart, entry.TrackedFrom, computedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) ForEachRawPacketForRestore(ctx context.Context, fn func(RawPacketRestoreRow) error) error {
	const q = `
SELECT packet_hash, payload_hex, observed_at
FROM raw_packets
ORDER BY observed_at ASC;
`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var row RawPacketRestoreRow
		var observedAtRaw string
		if err := rows.Scan(&row.PacketHash, &row.PayloadHex, &observedAtRaw); err != nil {
			return err
		}
		observedAt, err := time.Parse(time.RFC3339, observedAtRaw)
		if err != nil {
			return fmt.Errorf("parse observed_at for packet %s: %w", row.PacketHash, err)
		}
		row.ObservedAt = observedAt
		if err := fn(row); err != nil {
			return err
		}
	}
	return rows.Err()
}

func parseObserverNames(csv string) []string {
	value := strings.TrimSpace(csv)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
