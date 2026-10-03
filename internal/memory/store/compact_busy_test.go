package store

import (
	"database/sql"
	"strings"
	"testing"
)

func TestCompactRejectsBusyCheckpoint(t *testing.T) {
	db := testDB(t)
	if _, err := db.conn.Exec(`CREATE TABLE compact_probe (value INTEGER); INSERT INTO compact_probe VALUES (1); PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", db.path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tx, err := reader.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var value int
	if err := tx.QueryRow(`SELECT value FROM compact_probe`).Scan(&value); err != nil {
		t.Fatal(err)
	}
	if _, err := db.conn.Exec(`UPDATE compact_probe SET value=2`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Compact(); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("Compact error=%v, want busy checkpoint", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Compact(); err != nil {
		t.Fatalf("retry after reader closes: %v", err)
	}
	if err := db.conn.QueryRow(`SELECT value FROM compact_probe`).Scan(&value); err != nil || value != 2 {
		t.Fatalf("committed value changed: value=%d err=%v", value, err)
	}
}

func TestCompactRejectsReadOnly(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ro, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if _, _, err := ro.Compact(); err == nil {
		t.Fatal("readonly compaction succeeded")
	}
}
