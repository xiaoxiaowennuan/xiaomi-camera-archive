package scanner

import (
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mijia-archive/internal/archive"
	"mijia-archive/internal/store"
)

type fakeProbe struct{ calls int }

func (p *fakeProbe) Probe(context.Context, string) (archive.ProbeResult, error) {
	p.calls++
	return archive.ProbeResult{DurationMS: 60_000, VideoCodec: "hevc", AudioCodec: "pcm_alaw", Width: 2560, Height: 1440, FPS: 10}, nil
}

func TestScanIsIdempotentAndIgnoresSymlink(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	video := filepath.Join(root, "MIJIA_RECORD_VIDEO", "2024010203")
	motion := filepath.Join(root, "MIJIA_RECORD_MOTION", "2024010203")
	if err := os.MkdirAll(video, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(motion, 0o700); err != nil {
		t.Fatal(err)
	}
	mp4 := filepath.Join(video, "04M05S_1704135845.mp4")
	if err := os.WriteFile(mp4, []byte("synthetic"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(video, "04M05S_1704135845.jpeg"), []byte("jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(mp4, filepath.Join(video, "05M05S_1704135905.mp4"))
	b := make([]byte, 160)
	copy(b, "IMIEVENT")
	binary.LittleEndian.PutUint32(b[128:], 1704135845)
	if err := os.WriteFile(filepath.Join(motion, "motion_record_msg"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(state, "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	p := &fakeProbe{}
	folder, err := db.EnsureBootstrapFolder(context.Background(), "Test", root, root)
	if err != nil {
		t.Fatal(err)
	}
	s := Scanner{FolderID: folder.ID, MediaRoot: root, Location: loc, Store: db, Prober: p}
	first, err := s.Scan(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Scan(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	if first.Segments != 1 || first.Events != 1 || second.Reused != 1 || p.calls != 1 {
		t.Fatalf("first=%+v second=%+v calls=%d", first, second, p.calls)
	}
	segments, events, err := db.Counts(context.Background(), folder.ID)
	if err != nil || segments != 1 || events != 1 {
		t.Fatalf("counts %d %d %v", segments, events, err)
	}
	if err := os.Remove(mp4); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scan(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	segments, events, err = db.Counts(context.Background(), folder.ID)
	if err != nil || segments != 0 || events != 0 {
		t.Fatalf("missing input was not soft-deleted: %d %d %v", segments, events, err)
	}
}

func TestScanNASLayouts(t *testing.T) {
	for _, rel := range []string{
		"2026081007/12M56S_1786317176.mp4",
		"00_20260920162802_20260920163344.mp4",
	} {
		t.Run(rel, func(t *testing.T) {
			root, state := t.TempDir(), t.TempDir()
			file := filepath.Join(root, rel)
			if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte("synthetic"), 0o600); err != nil {
				t.Fatal(err)
			}
			db, err := store.Open(filepath.Join(state, "archive.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			folder, err := db.EnsureBootstrapFolder(context.Background(), "Test", root, root)
			if err != nil {
				t.Fatal(err)
			}
			loc := time.FixedZone("CST", 8*3600)
			p := &fakeProbe{}
			scan := Scanner{FolderID: folder.ID, MediaRoot: root, Location: loc, Store: db, Prober: p}
			first, err := scan.Scan(context.Background(), state)
			if err != nil {
				t.Fatal(err)
			}
			second, err := scan.Scan(context.Background(), state)
			if err != nil || first.Segments != 1 || second.Reused != 1 || p.calls != 1 {
				t.Fatalf("first=%+v second=%+v err=%v", first, second, err)
			}
			if !archive.HasVideoLayout(root) {
				t.Fatal("camera folder not recognized")
			}
		})
	}
}
