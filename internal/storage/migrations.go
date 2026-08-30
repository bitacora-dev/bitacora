package storage

import "database/sql"

// schemaMigrations runs in order against a freshly opened month database.
// Every statement is IF NOT EXISTS so re-running it is always safe — there
// is no separate "already migrated" bookkeeping yet, since there's exactly
// one migration.
var schemaMigrations = []string{
	`CREATE TABLE IF NOT EXISTS events (
		id            TEXT PRIMARY KEY,
		ts            INTEGER NOT NULL,
		ts_received   INTEGER,
		host_id       TEXT NOT NULL,
		source        TEXT NOT NULL,
		type          TEXT NOT NULL,
		severity      TEXT NOT NULL,
		title         TEXT NOT NULL,
		subject_json  TEXT,
		attrs_json    TEXT,
		fingerprint   TEXT,
		log_refs_json TEXT,
		schema        INTEGER NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_events_ts ON events(ts)`,
	`CREATE INDEX IF NOT EXISTS idx_events_host_ts ON events(host_id, ts)`,
	// FTS5 index over event titles. Kept in sync manually at insert time
	// (see insertEvent) rather than via triggers, to keep the write path
	// in one place.
	`CREATE VIRTUAL TABLE IF NOT EXISTS events_fts USING fts5(id UNINDEXED, title)`,
}

func migrate(db *sql.DB) error {
	for _, stmt := range schemaMigrations {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
