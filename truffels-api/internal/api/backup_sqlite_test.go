package api

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestSnapshotSQLiteProducesReadableCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.db")
	db, err := sql.Open("sqlite", src)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = db.Close() }()

	if _, err := db.Exec(`CREATE TABLE services (id TEXT PRIMARY KEY, enabled INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO services VALUES ('digibyted', 1)`); err != nil {
		t.Fatalf("insert: %v", err)
	}

	dest := filepath.Join(dir, "snap.db")
	if err := snapshotSQLite(db, dest); err != nil {
		t.Fatalf("snapshotSQLite: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("Snapshot missing: %v", err)
	}

	copyDB, err := sql.Open("sqlite", dest)
	if err != nil {
		t.Fatalf("open copy: %v", err)
	}
	defer func() { _ = copyDB.Close() }()
	var enabled int
	if err := copyDB.QueryRow(`SELECT enabled FROM services WHERE id='digibyted'`).Scan(&enabled); err != nil {
		t.Fatalf("Snapshot not readable: %v", err)
	}
	if enabled != 1 {
		t.Errorf("enabled = %d, expected 1", enabled)
	}
}

func TestSnapshotSQLiteRefusesExistingDest(t *testing.T) {
	dir := t.TempDir()
	db, _ := sql.Open("sqlite", filepath.Join(dir, "s.db"))
	defer func() { _ = db.Close() }()
	_, _ = db.Exec(`CREATE TABLE t (x INTEGER)`)

	dest := filepath.Join(dir, "existing.db")
	if err := os.WriteFile(dest, []byte("do not overwrite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := snapshotSQLite(db, dest); err == nil {
		t.Fatal("expected error for existing destination file")
	}
}

func TestSnapshotWithStaleStagingCleanup(t *testing.T) {
	dir := t.TempDir()
	db, _ := sql.Open("sqlite", filepath.Join(dir, "s.db"))
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE t (x INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO t VALUES (42)`); err != nil {
		t.Fatal(err)
	}

	stagingRoot := filepath.Join(dir, "staging")
	stagingDataDir := filepath.Join(stagingRoot, "data", "truffels")

	// Simulate a stale file left from a previous failed backup
	if err := os.MkdirAll(stagingDataDir, 0o750); err != nil {
		t.Fatal(err)
	}
	staleFile := filepath.Join(stagingDataDir, "truffels.db")
	if err := os.WriteFile(staleFile, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Clean up stale staging (as the handler should do)
	if err := os.RemoveAll(stagingRoot); err != nil {
		t.Fatal(err)
	}

	// Re-create the directory structure
	if err := os.MkdirAll(stagingDataDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// Now snapshot should succeed
	snapPath := filepath.Join(stagingDataDir, "truffels.db")
	if err := snapshotSQLite(db, snapPath); err != nil {
		t.Fatalf("snapshotSQLite after cleanup: %v", err)
	}

	// Verify the snapshot is readable and contains the data
	copyDB, err := sql.Open("sqlite", snapPath)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = copyDB.Close() }()
	var val int
	if err := copyDB.QueryRow(`SELECT x FROM t WHERE x = 42`).Scan(&val); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if val != 42 {
		t.Errorf("value = %d, expected 42", val)
	}
}
