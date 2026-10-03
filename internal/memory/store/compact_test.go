package store

import (
	"fmt"
	"testing"
)

func TestCompact_ShrinksAfterDeletes(t *testing.T) {
	db, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.conn.Exec(`CREATE TABLE filler (b BLOB)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, err := db.conn.Exec(`INSERT INTO filler VALUES (zeroblob(8192))`); err != nil {
			t.Fatal(fmt.Errorf("insert %d: %w", i, err))
		}
	}
	if _, err := db.conn.Exec(`DELETE FROM filler`); err != nil {
		t.Fatal(err)
	}
	before, after, err := db.Compact()
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if after >= before {
		t.Fatalf("size after = %d, want below %d", after, before)
	}
	var free int
	if err := db.conn.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		t.Fatal(err)
	}
	if free != 0 {
		t.Fatalf("freelist_count = %d, want 0", free)
	}
}
