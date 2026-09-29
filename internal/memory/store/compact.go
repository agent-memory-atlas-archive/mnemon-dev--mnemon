package store

import (
	"fmt"
	"os"
)

// Compact rewrites the database with VACUUM and returns its size in bytes
// before and after. Recall updates access counters on every hit, so pages of
// the insights and edges tables end up scattered across the file; a cold recall
// then reads them at random-I/O speed. VACUUM restores table order and drops
// free pages. It needs an exclusive lock and briefly doubles disk usage.
func (db *DB) Compact() (before, after int64, err error) {
	if db.readOnly {
		return 0, 0, fmt.Errorf("compact: database is read-only")
	}
	if _, err := db.conn.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return 0, 0, fmt.Errorf("checkpoint: %w", err)
	}
	before = db.fileSize()
	if _, err := db.conn.Exec(`VACUUM`); err != nil {
		return 0, 0, fmt.Errorf("vacuum: %w", err)
	}
	if _, err := db.conn.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return 0, 0, fmt.Errorf("checkpoint: %w", err)
	}
	return before, db.fileSize(), nil
}

func (db *DB) fileSize() int64 {
	fi, err := os.Stat(db.path)
	if err != nil {
		return 0
	}
	return fi.Size()
}
