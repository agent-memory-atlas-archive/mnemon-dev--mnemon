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
	if err := db.compactCheckpoint(); err != nil {
		return 0, 0, fmt.Errorf("checkpoint: %w", err)
	}
	before, err = db.fileSize()
	if err != nil {
		return 0, 0, err
	}
	if _, err := db.conn.Exec(`VACUUM`); err != nil {
		return 0, 0, fmt.Errorf("vacuum: %w", err)
	}
	if err := db.compactCheckpoint(); err != nil {
		return before, 0, fmt.Errorf("vacuum completed but checkpoint failed: %w", err)
	}
	after, err = db.fileSize()
	return before, after, err
}

func (db *DB) compactCheckpoint() error {
	var busy, log, checkpointed int
	if err := db.conn.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &checkpointed); err != nil {
		return err
	}
	if busy != 0 {
		return fmt.Errorf("database busy: checkpointed %d of %d WAL frames; retry after active transactions finish", checkpointed, log)
	}
	return nil
}

func (db *DB) fileSize() (int64, error) {
	fi, err := os.Stat(db.path)
	if err != nil {
		return 0, fmt.Errorf("stat database: %w", err)
	}
	return fi.Size(), nil
}
