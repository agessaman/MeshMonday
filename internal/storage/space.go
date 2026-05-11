package storage

import (
	"context"
	"database/sql"
	"os"
)

// SpaceStats summarizes on-disk and logical SQLite size (post-delete freelist is often huge until VACUUM).
type SpaceStats struct {
	PageSize         int
	PageCount        int
	FreelistCount    int
	FreelistBytes    int64
	FileSizeBytes    int64
	WALSizeBytes     int64
	MainDatabasePath string
}

// CheckpointWal tries to move WAL frames into the DB file and truncate the -wal sidecar.
// Safe to call while serving; may no-op if readers prevent TRUNCATE.
func (s *SQLiteStore) CheckpointWal(ctx context.Context) {
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		_, _ = s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)")
	}
}

func (s *SQLiteStore) mainDatabasePath(ctx context.Context) (string, error) {
	rows, err := s.db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name string
		var file sql.NullString
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return "", err
		}
		if name == "main" && file.Valid && file.String != "" {
			return file.String, nil
		}
	}
	return "", rows.Err()
}

// SpaceStats reads pragma page accounting and optional filesystem sizes for the main DB and WAL.
func (s *SQLiteStore) SpaceStats(ctx context.Context) (SpaceStats, error) {
	var st SpaceStats
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_size").Scan(&st.PageSize); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA page_count").Scan(&st.PageCount); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, "PRAGMA freelist_count").Scan(&st.FreelistCount); err != nil {
		return st, err
	}
	st.FreelistBytes = int64(st.FreelistCount * st.PageSize)

	p, err := s.mainDatabasePath(ctx)
	if err != nil {
		return st, err
	}
	st.MainDatabasePath = p
	if p == "" {
		return st, nil
	}
	if fi, err := os.Stat(p); err == nil {
		st.FileSizeBytes = fi.Size()
	}
	if fi, err := os.Stat(p + "-wal"); err == nil {
		st.WALSizeBytes = fi.Size()
	}
	return st, nil
}

// AfterPruneMaintenance checkpoints WAL then returns stats so logs can show reclaimable freelist.
func (s *SQLiteStore) AfterPruneMaintenance(ctx context.Context) (SpaceStats, error) {
	s.CheckpointWal(ctx)
	return s.SpaceStats(ctx)
}
