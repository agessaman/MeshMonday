package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"meshmonday/internal/checkins"
)

const (
	pruneScanBatchSize     = 400
	pruneDeleteChunk       = 50
	pruneYieldAfterDeletes = 20 * time.Millisecond
	retentionBusyMax       = 8
	retentionBusyDelay     = 50 * time.Millisecond
	vacuumHintThreshold    = 10000
)

// PruneRawPackets deletes raw_packets not required for check-ins and not within
// retained Monday ambient history. Cascades packet_observations and checkin_packets.
// retainMondayWeeks: 0 keeps only checkin-linked raw rows (no extra Monday traffic).
func (s *SQLiteStore) PruneRawPackets(ctx context.Context, now time.Time, tz string, retainMondayWeeks int) (deleted int64, err error) {
	cutoffWeekStart := checkins.WeekStartMonday(now, tz).AddDate(0, 0, -7*retainMondayWeeks)

	protected, err := s.protectedPacketHashes(ctx)
	if err != nil {
		return 0, err
	}

	var lastID int64
	var total int64

	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}

		rows, err := s.scanRawPacketBatch(ctx, lastID, pruneScanBatchSize)
		if err != nil {
			return total, err
		}
		if len(rows) == 0 {
			break
		}

		var toDelete []int64
		maxID := rows[len(rows)-1].id
		for _, r := range rows {
			if s.shouldRetainRawPacket(r.packetHash, r.observedAt, tz, cutoffWeekStart, protected) {
				continue
			}
			toDelete = append(toDelete, r.id)
		}

		if len(toDelete) == 0 {
			lastID = maxID
			continue
		}

		n, err := s.deleteRawPacketIDs(ctx, toDelete)
		if err != nil {
			return total, err
		}
		total += n
		if n > 0 {
			select {
			case <-ctx.Done():
				return total, ctx.Err()
			case <-time.After(pruneYieldAfterDeletes):
			}
		}
		// Re-scan from same lastID: freed ids leave gaps; next batch must not skip rows.
	}

	return total, nil
}

type rawPacketScanRow struct {
	id         int64
	packetHash string
	observedAt time.Time
}

func (s *SQLiteStore) scanRawPacketBatch(ctx context.Context, afterID int64, limit int) ([]rawPacketScanRow, error) {
	const q = `
SELECT id, packet_hash, observed_at
FROM raw_packets
WHERE id > ?
ORDER BY id
LIMIT ?;
`
	rows, err := s.db.QueryContext(ctx, q, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []rawPacketScanRow
	for rows.Next() {
		var r rawPacketScanRow
		var observedAt string
		if err := rows.Scan(&r.id, &r.packetHash, &observedAt); err != nil {
			return nil, err
		}
		t, err := time.Parse(time.RFC3339, observedAt)
		if err != nil {
			return nil, fmt.Errorf("parse observed_at id=%d: %w", r.id, err)
		}
		r.observedAt = t
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) protectedPacketHashes(ctx context.Context) (map[string]struct{}, error) {
	const q = `
SELECT packet_hash FROM checkins
UNION
SELECT packet_hash FROM checkin_packets;
`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	m := make(map[string]struct{})
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		m[h] = struct{}{}
	}
	return m, rows.Err()
}

func (s *SQLiteStore) shouldRetainRawPacket(packetHash string, observedAt time.Time, tz string, cutoffWeekStart time.Time, protected map[string]struct{}) bool {
	if _, ok := protected[packetHash]; ok {
		return true
	}
	if !checkins.IsMondayInTZ(observedAt, tz) {
		return false
	}
	ws := checkins.WeekStartMonday(observedAt, tz)
	return !ws.Before(cutoffWeekStart)
}

func (s *SQLiteStore) deleteRawPacketIDs(ctx context.Context, ids []int64) (int64, error) {
	var total int64
	for start := 0; start < len(ids); start += pruneDeleteChunk {
		end := start + pruneDeleteChunk
		if end > len(ids) {
			end = len(ids)
		}
		chunk := ids[start:end]
		placeholders := strings.Repeat("?,", len(chunk))
		placeholders = strings.TrimSuffix(placeholders, ",")
		q := fmt.Sprintf(`DELETE FROM raw_packets WHERE id IN (%s);`, placeholders)
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		var res sql.Result
		var err error
		for attempt := 0; attempt <= retentionBusyMax; attempt++ {
			res, err = s.db.ExecContext(ctx, q, args...)
			if err == nil {
				break
			}
			if !isSQLiteBusyError(err) || attempt == retentionBusyMax {
				return total, err
			}
			select {
			case <-ctx.Done():
				return total, ctx.Err()
			case <-time.After(retentionBusyDelay):
			}
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(pruneYieldAfterDeletes):
		}
	}
	return total, nil
}

func isSQLiteBusyError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToUpper(err.Error())
	return strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "DATABASE IS LOCKED")
}

// VacuumHintThreshold is the delete count above which operators may want to run VACUUM.
func VacuumHintThreshold() int64 { return vacuumHintThreshold }
