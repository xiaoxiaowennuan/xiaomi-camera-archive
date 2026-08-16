package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestOpenMigratesSingleFolderDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `
CREATE TABLE scan_runs (id INTEGER PRIMARY KEY,started_at INTEGER NOT NULL,finished_at INTEGER,status TEXT NOT NULL,error_summary TEXT NOT NULL DEFAULT '');
CREATE TABLE segments (id INTEGER PRIMARY KEY,public_id TEXT NOT NULL UNIQUE,video_path TEXT NOT NULL UNIQUE,thumbnail_path TEXT NOT NULL DEFAULT '',start_ms INTEGER NOT NULL,duration_ms INTEGER NOT NULL,size_bytes INTEGER NOT NULL,mtime_ns INTEGER NOT NULL,video_codec TEXT NOT NULL DEFAULT '',audio_codec TEXT NOT NULL DEFAULT '',width INTEGER NOT NULL DEFAULT 0,height INTEGER NOT NULL DEFAULT 0,fps REAL NOT NULL DEFAULT 0,probe_status TEXT NOT NULL,probe_error TEXT NOT NULL DEFAULT '',available INTEGER NOT NULL DEFAULT 1,last_scan_id INTEGER NOT NULL,missing_at INTEGER);
CREATE TABLE events (id INTEGER PRIMARY KEY,public_id TEXT NOT NULL UNIQUE,segment_id INTEGER NOT NULL,start_ms INTEGER NOT NULL,end_ms INTEGER NOT NULL,raw_record_count INTEGER NOT NULL,has_unknown_flag INTEGER NOT NULL,last_scan_id INTEGER NOT NULL,UNIQUE(segment_id,start_ms));
CREATE TABLE transcode_entries (segment_id INTEGER NOT NULL,profile TEXT NOT NULL,fingerprint TEXT NOT NULL,cache_path TEXT NOT NULL,status TEXT NOT NULL,size_bytes INTEGER NOT NULL DEFAULT 0,last_access_ms INTEGER NOT NULL,error_code TEXT NOT NULL DEFAULT '',PRIMARY KEY(segment_id,profile));
INSERT INTO scan_runs VALUES(1,1,2,'complete','');
INSERT INTO segments VALUES(1,'0123456789abcdef0123456789abcdef','hour/file.mp4','',1000,60000,1,1,'hevc','pcm_alaw',2560,1440,10,'ready','',1,1,NULL);
`
	if _, err := db.Exec(legacy); err != nil {
		t.Fatal(err)
	}
	db.Close()

	archive, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	folders, err := archive.Folders(context.Background())
	if err != nil || len(folders) != 1 {
		t.Fatalf("folders=%v err=%v", folders, err)
	}
	segments, events, err := archive.Counts(context.Background(), folders[0].ID)
	if err != nil || segments != 1 || events != 0 {
		t.Fatalf("segments=%d events=%d err=%v", segments, events, err)
	}
}

func TestUpdatePasswordInvalidatesSessions(t *testing.T) {
	archive, err := Open(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	if err := archive.EnsureAdmin(context.Background(), "admin", "old-hash"); err != nil {
		t.Fatal(err)
	}
	if err := archive.Commit("session-token", []byte("session-data"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := archive.UpdatePassword(context.Background(), "admin", "new-hash"); err != nil {
		t.Fatal(err)
	}
	user, err := archive.UserByUsername(context.Background(), "admin")
	if err != nil || user.PasswordHash != "new-hash" {
		t.Fatalf("password hash was not updated: user=%+v err=%v", user, err)
	}
	if _, found, err := archive.Find("session-token"); err != nil || found {
		t.Fatalf("session still exists: found=%v err=%v", found, err)
	}
}
