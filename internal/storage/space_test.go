package storage

import (
	"context"
	"testing"
)

func TestSpaceStatsAfterOpen(t *testing.T) {
	path := t.TempDir() + "/space.db"
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	st, err := store.SpaceStats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.PageSize <= 0 || st.PageCount <= 0 {
		t.Fatalf("expected positive page stats, got %+v", st)
	}
}
