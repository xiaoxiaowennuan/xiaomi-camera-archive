package store

import (
	"database/sql"
	"fmt"
	"time"
)

func hasColumn(db *sql.DB, table, column string) (bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, kind string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &primaryKey); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func migrateFolders(db *sql.DB) error {
	now := time.Now().UnixMilli()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM folders`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		var segmentCount int
		if err := db.QueryRow(`SELECT count(*) FROM segments`).Scan(&segmentCount); err != nil {
			return err
		}
		scanStatus := "pending"
		if segmentCount > 0 {
			scanStatus = "ready"
		}
		id, err := publicID()
		if err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO folders(public_id,name,server_path,mount_path,enabled,scan_status,created_at,updated_at) VALUES(?, 'Default archive', '', '/media', 1, ?, ?, ?)`, id, scanStatus, now, now); err != nil {
			return err
		}
	}

	for _, column := range []struct {
		name string
		sql  string
	}{
		{"scan_status", `ALTER TABLE folders ADD COLUMN scan_status TEXT NOT NULL DEFAULT 'pending'`},
		{"last_scan_at", `ALTER TABLE folders ADD COLUMN last_scan_at INTEGER`},
		{"scan_error", `ALTER TABLE folders ADD COLUMN scan_error TEXT NOT NULL DEFAULT ''`},
	} {
		has, err := hasColumn(db, "folders", column.name)
		if err != nil {
			return err
		}
		if !has {
			if _, err := db.Exec(column.sql); err != nil {
				return err
			}
		}
	}

	hasFolderID, err := hasColumn(db, "segments", "folder_id")
	if err != nil {
		return err
	}
	if hasFolderID {
		_, err = db.Exec(`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (3, ?)`, now)
		return err
	}

	if _, err := db.Exec(`PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	defer db.Exec(`PRAGMA foreign_keys=ON`)
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	statements := []string{
		`CREATE TABLE scan_runs_new (
          id INTEGER PRIMARY KEY, folder_id INTEGER NOT NULL, started_at INTEGER NOT NULL,
          finished_at INTEGER, status TEXT NOT NULL, error_summary TEXT NOT NULL DEFAULT '',
          FOREIGN KEY(folder_id) REFERENCES folders(id)
        )`,
		`INSERT INTO scan_runs_new(id,folder_id,started_at,finished_at,status,error_summary) SELECT id,1,started_at,finished_at,status,error_summary FROM scan_runs`,
		`CREATE TABLE segments_new (
          id INTEGER PRIMARY KEY, public_id TEXT NOT NULL UNIQUE, folder_id INTEGER NOT NULL,
          video_path TEXT NOT NULL, thumbnail_path TEXT NOT NULL DEFAULT '', start_ms INTEGER NOT NULL,
          duration_ms INTEGER NOT NULL, size_bytes INTEGER NOT NULL, mtime_ns INTEGER NOT NULL,
          video_codec TEXT NOT NULL DEFAULT '', audio_codec TEXT NOT NULL DEFAULT '', width INTEGER NOT NULL DEFAULT 0,
          height INTEGER NOT NULL DEFAULT 0, fps REAL NOT NULL DEFAULT 0, probe_status TEXT NOT NULL,
          probe_error TEXT NOT NULL DEFAULT '', available INTEGER NOT NULL DEFAULT 1, last_scan_id INTEGER NOT NULL,
          missing_at INTEGER, UNIQUE(folder_id,video_path), FOREIGN KEY(folder_id) REFERENCES folders(id),
          FOREIGN KEY(last_scan_id) REFERENCES scan_runs_new(id)
        )`,
		`INSERT INTO segments_new(id,public_id,folder_id,video_path,thumbnail_path,start_ms,duration_ms,size_bytes,mtime_ns,video_codec,audio_codec,width,height,fps,probe_status,probe_error,available,last_scan_id,missing_at)
         SELECT id,public_id,1,video_path,thumbnail_path,start_ms,duration_ms,size_bytes,mtime_ns,video_codec,audio_codec,width,height,fps,probe_status,probe_error,available,last_scan_id,missing_at FROM segments`,
		`CREATE TABLE events_new (
          id INTEGER PRIMARY KEY, public_id TEXT NOT NULL UNIQUE, segment_id INTEGER NOT NULL,
          start_ms INTEGER NOT NULL, end_ms INTEGER NOT NULL, raw_record_count INTEGER NOT NULL,
          has_unknown_flag INTEGER NOT NULL, last_scan_id INTEGER NOT NULL, UNIQUE(segment_id,start_ms),
          FOREIGN KEY(segment_id) REFERENCES segments_new(id), FOREIGN KEY(last_scan_id) REFERENCES scan_runs_new(id)
        )`,
		`INSERT INTO events_new SELECT * FROM events`,
		`CREATE TABLE transcode_entries_new (
          segment_id INTEGER NOT NULL, profile TEXT NOT NULL, fingerprint TEXT NOT NULL, cache_path TEXT NOT NULL,
          status TEXT NOT NULL, size_bytes INTEGER NOT NULL DEFAULT 0, last_access_ms INTEGER NOT NULL,
          error_code TEXT NOT NULL DEFAULT '', PRIMARY KEY(segment_id,profile), FOREIGN KEY(segment_id) REFERENCES segments_new(id)
        )`,
		`INSERT INTO transcode_entries_new SELECT * FROM transcode_entries`,
		`DROP TABLE transcode_entries`, `DROP TABLE events`, `DROP TABLE segments`, `DROP TABLE scan_runs`,
		`ALTER TABLE scan_runs_new RENAME TO scan_runs`,
		`ALTER TABLE segments_new RENAME TO segments`,
		`ALTER TABLE events_new RENAME TO events`,
		`ALTER TABLE transcode_entries_new RENAME TO transcode_entries`,
		`CREATE INDEX segments_time ON segments(folder_id,start_ms,available,probe_status)`,
		`CREATE INDEX events_time ON events(start_ms)`,
		`INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (3, unixepoch('now') * 1000)`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("folder migration: %w", err)
		}
	}
	return tx.Commit()
}
