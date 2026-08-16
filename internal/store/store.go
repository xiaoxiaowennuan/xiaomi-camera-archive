package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"mijia-archive/internal/archive"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

const schema = `
PRAGMA foreign_keys = ON;
CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (1, unixepoch('now') * 1000);
CREATE TABLE IF NOT EXISTS scan_runs (
  id INTEGER PRIMARY KEY, started_at INTEGER NOT NULL, finished_at INTEGER, status TEXT NOT NULL, error_summary TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS segments (
  id INTEGER PRIMARY KEY, public_id TEXT NOT NULL UNIQUE, video_path TEXT NOT NULL UNIQUE, thumbnail_path TEXT NOT NULL DEFAULT '',
  start_ms INTEGER NOT NULL, duration_ms INTEGER NOT NULL, size_bytes INTEGER NOT NULL, mtime_ns INTEGER NOT NULL,
  video_codec TEXT NOT NULL DEFAULT '', audio_codec TEXT NOT NULL DEFAULT '', width INTEGER NOT NULL DEFAULT 0,
  height INTEGER NOT NULL DEFAULT 0, fps REAL NOT NULL DEFAULT 0, probe_status TEXT NOT NULL,
  probe_error TEXT NOT NULL DEFAULT '', available INTEGER NOT NULL DEFAULT 1, last_scan_id INTEGER NOT NULL,
  missing_at INTEGER, FOREIGN KEY(last_scan_id) REFERENCES scan_runs(id)
);
CREATE INDEX IF NOT EXISTS segments_time ON segments(start_ms, available, probe_status);
CREATE TABLE IF NOT EXISTS events (
  id INTEGER PRIMARY KEY, public_id TEXT NOT NULL UNIQUE, segment_id INTEGER NOT NULL,
  start_ms INTEGER NOT NULL, end_ms INTEGER NOT NULL, raw_record_count INTEGER NOT NULL,
  has_unknown_flag INTEGER NOT NULL, last_scan_id INTEGER NOT NULL,
  UNIQUE(segment_id, start_ms), FOREIGN KEY(segment_id) REFERENCES segments(id), FOREIGN KEY(last_scan_id) REFERENCES scan_runs(id)
);
CREATE INDEX IF NOT EXISTS events_time ON events(start_ms);
CREATE TABLE IF NOT EXISTS transcode_entries (
  segment_id INTEGER NOT NULL, profile TEXT NOT NULL, fingerprint TEXT NOT NULL, cache_path TEXT NOT NULL,
  status TEXT NOT NULL, size_bytes INTEGER NOT NULL DEFAULT 0, last_access_ms INTEGER NOT NULL,
  error_code TEXT NOT NULL DEFAULT '', PRIMARY KEY(segment_id, profile), FOREIGN KEY(segment_id) REFERENCES segments(id)
);
INSERT OR IGNORE INTO schema_migrations(version, applied_at) VALUES (2, unixepoch('now') * 1000);
CREATE TABLE IF NOT EXISTS folders (
  id INTEGER PRIMARY KEY, public_id TEXT NOT NULL UNIQUE, name TEXT NOT NULL,
  server_path TEXT NOT NULL DEFAULT '', mount_path TEXT NOT NULL UNIQUE,
  enabled INTEGER NOT NULL DEFAULT 1, scan_status TEXT NOT NULL DEFAULT 'pending',
  last_scan_at INTEGER, scan_error TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY, public_id TEXT NOT NULL UNIQUE, username TEXT NOT NULL COLLATE NOCASE UNIQUE,
  password_hash TEXT NOT NULL, role TEXT NOT NULL CHECK(role IN ('admin','user')),
  active INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY, data BLOB NOT NULL, expiry INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS folders_server_path ON folders(server_path) WHERE server_path<>'';
CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions(expiry);
`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA busy_timeout=5000;" + schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateFolders(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

type Existing struct {
	SizeBytes, MTimeNS int64
	Probe              archive.ProbeResult
	Status, ProbeError string
}

func (s *Store) Existing(ctx context.Context, folderID int64, rel string) (Existing, bool, error) {
	var e Existing
	err := s.db.QueryRowContext(ctx, `SELECT size_bytes,mtime_ns,duration_ms,video_codec,audio_codec,width,height,fps,probe_status,probe_error FROM segments WHERE folder_id=? AND video_path=?`, folderID, rel).
		Scan(&e.SizeBytes, &e.MTimeNS, &e.Probe.DurationMS, &e.Probe.VideoCodec, &e.Probe.AudioCodec, &e.Probe.Width, &e.Probe.Height, &e.Probe.FPS, &e.Status, &e.ProbeError)
	if errors.Is(err, sql.ErrNoRows) {
		return Existing{}, false, nil
	}
	return e, err == nil, err
}

func publicID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Store) ApplyScan(ctx context.Context, folderID int64, segments []archive.Segment, events []archive.Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UnixMilli()
	res, err := tx.ExecContext(ctx, `INSERT INTO scan_runs(folder_id,started_at,status) VALUES(?, ?, 'running')`, folderID, now)
	if err != nil {
		return err
	}
	scanID, _ := res.LastInsertId()
	for _, seg := range segments {
		id, err := publicID()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO segments(public_id,folder_id,video_path,thumbnail_path,start_ms,duration_ms,size_bytes,mtime_ns,video_codec,audio_codec,width,height,fps,probe_status,probe_error,available,last_scan_id,missing_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,1,?,NULL)
		ON CONFLICT(folder_id,video_path) DO UPDATE SET thumbnail_path=excluded.thumbnail_path,start_ms=excluded.start_ms,duration_ms=excluded.duration_ms,size_bytes=excluded.size_bytes,mtime_ns=excluded.mtime_ns,video_codec=excluded.video_codec,audio_codec=excluded.audio_codec,width=excluded.width,height=excluded.height,fps=excluded.fps,probe_status=excluded.probe_status,probe_error=excluded.probe_error,available=1,last_scan_id=excluded.last_scan_id,missing_at=NULL`,
			id, folderID, seg.VideoPath, seg.ThumbnailPath, seg.StartMS, seg.DurationMS, seg.SizeBytes, seg.MTimeNS, seg.VideoCodec, seg.AudioCodec, seg.Width, seg.Height, seg.FPS, seg.ProbeStatus, seg.ProbeError, scanID)
		if err != nil {
			return fmt.Errorf("upsert segment: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE segments SET available=0,missing_at=? WHERE folder_id=? AND last_scan_id<>? AND available=1`, now, folderID, scanID); err != nil {
		return err
	}
	for _, event := range events {
		id, err := publicID()
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO events(public_id,segment_id,start_ms,end_ms,raw_record_count,has_unknown_flag,last_scan_id)
		SELECT ?,id,?,start_ms+duration_ms,?,?,? FROM segments WHERE folder_id=? AND start_ms=? AND available=1
		ON CONFLICT(segment_id,start_ms) DO UPDATE SET end_ms=excluded.end_ms,raw_record_count=excluded.raw_record_count,has_unknown_flag=excluded.has_unknown_flag,last_scan_id=excluded.last_scan_id`,
			id, event.StartMS, event.RawRecordCount, event.HasUnknownFlag, scanID, folderID, event.StartMS)
		if err != nil {
			return fmt.Errorf("upsert event: %w", err)
		}
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM events WHERE segment_id IN (SELECT id FROM segments WHERE folder_id=?) AND last_scan_id<>?`, folderID, scanID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE scan_runs SET finished_at=?,status='complete' WHERE id=?`, time.Now().UnixMilli(), scanID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Counts(ctx context.Context, folderID int64) (segments, events int, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM segments WHERE folder_id=? AND available=1`, folderID).Scan(&segments)
	if err == nil {
		err = s.db.QueryRowContext(ctx, `SELECT count(*) FROM events WHERE segment_id IN (SELECT id FROM segments WHERE folder_id=?)`, folderID).Scan(&events)
	}
	return
}

type SegmentRecord struct {
	PublicID, VideoPath, ThumbnailPath, VideoCodec, AudioCodec string
	MountPath                                                  string
	FolderID                                                   int64
	StartMS, DurationMS, SizeBytes, MTimeNS                    int64
	Width, Height                                              int
	FPS                                                        float64
}

type EventRecord struct {
	PublicID       string `json:"id"`
	StartMS        int64  `json:"startMs"`
	EndMS          int64  `json:"endMs"`
	RawRecordCount int    `json:"rawRecordCount"`
	HasUnknownFlag bool   `json:"hasUnknownFlag"`
}

func scanSegment(row interface{ Scan(...any) error }) (SegmentRecord, error) {
	var r SegmentRecord
	err := row.Scan(&r.PublicID, &r.VideoPath, &r.ThumbnailPath, &r.StartMS, &r.DurationMS, &r.SizeBytes, &r.MTimeNS, &r.VideoCodec, &r.AudioCodec, &r.Width, &r.Height, &r.FPS, &r.MountPath, &r.FolderID)
	return r, err
}

func (s *Store) Segment(ctx context.Context, publicID string) (SegmentRecord, error) {
	return scanSegment(s.db.QueryRowContext(ctx, `SELECT s.public_id,s.video_path,s.thumbnail_path,s.start_ms,s.duration_ms,s.size_bytes,s.mtime_ns,s.video_codec,s.audio_codec,s.width,s.height,s.fps,f.mount_path,f.id FROM segments s JOIN folders f ON f.id=s.folder_id WHERE s.public_id=? AND s.available=1 AND s.probe_status='ready' AND f.enabled=1`, publicID))
}

func (s *Store) Timeline(ctx context.Context, folderID, startMS, endMS int64) ([]SegmentRecord, []EventRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.public_id,s.video_path,s.thumbnail_path,s.start_ms,s.duration_ms,s.size_bytes,s.mtime_ns,s.video_codec,s.audio_codec,s.width,s.height,s.fps,f.mount_path,f.id FROM segments s JOIN folders f ON f.id=s.folder_id WHERE s.folder_id=? AND s.available=1 AND s.probe_status='ready' AND s.start_ms+s.duration_ms>? AND s.start_ms<? ORDER BY s.start_ms`, folderID, startMS, endMS)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var segments []SegmentRecord
	for rows.Next() {
		r, err := scanSegment(rows)
		if err != nil {
			return nil, nil, err
		}
		segments = append(segments, r)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	eRows, err := s.db.QueryContext(ctx, `SELECT e.public_id,e.start_ms,e.end_ms,e.raw_record_count,e.has_unknown_flag FROM events e JOIN segments s ON s.id=e.segment_id WHERE s.folder_id=? AND e.start_ms>=? AND e.start_ms<? ORDER BY e.start_ms`, folderID, startMS, endMS)
	if err != nil {
		return nil, nil, err
	}
	defer eRows.Close()
	var events []EventRecord
	for eRows.Next() {
		var e EventRecord
		if err := eRows.Scan(&e.PublicID, &e.StartMS, &e.EndMS, &e.RawRecordCount, &e.HasUnknownFlag); err != nil {
			return nil, nil, err
		}
		events = append(events, e)
	}
	return segments, events, eRows.Err()
}

func (s *Store) Events(ctx context.Context, folderID, startMS, endMS, cursor int64, limit int) ([]EventRecord, error) {
	if cursor > startMS {
		startMS = cursor
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.public_id,e.start_ms,e.end_ms,e.raw_record_count,e.has_unknown_flag FROM events e JOIN segments s ON s.id=e.segment_id WHERE s.folder_id=? AND e.start_ms>=? AND e.start_ms<? ORDER BY e.start_ms LIMIT ?`, folderID, startMS, endMS, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []EventRecord
	for rows.Next() {
		var e EventRecord
		if err := rows.Scan(&e.PublicID, &e.StartMS, &e.EndMS, &e.RawRecordCount, &e.HasUnknownFlag); err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

type DayRecord struct {
	Date       string `json:"date"`
	Segments   int    `json:"segments"`
	CoverageMS int64  `json:"coverageMs"`
	Events     int    `json:"events"`
}

func (s *Store) Days(ctx context.Context, folderID, fromMS, toMS int64, loc *time.Location) ([]DayRecord, error) {
	segments, events, err := s.Timeline(ctx, folderID, fromMS, toMS)
	if err != nil {
		return nil, err
	}
	by := map[string]*DayRecord{}
	for _, seg := range segments {
		d := time.UnixMilli(seg.StartMS).In(loc).Format("2006-01-02")
		if by[d] == nil {
			by[d] = &DayRecord{Date: d}
		}
		by[d].Segments++
		by[d].CoverageMS += seg.DurationMS
	}
	for _, event := range events {
		d := time.UnixMilli(event.StartMS).In(loc).Format("2006-01-02")
		if by[d] == nil {
			by[d] = &DayRecord{Date: d}
		}
		by[d].Events++
	}
	result := make([]DayRecord, 0, len(by))
	for _, d := range by {
		result = append(result, *d)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Date < result[j].Date })
	return result, nil
}
