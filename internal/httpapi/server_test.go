package httpapi

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mijia-archive/internal/archive"
	"mijia-archive/internal/auth"
	"mijia-archive/internal/media"
	"mijia-archive/internal/store"
	"mijia-archive/internal/transcode"
)

func TestIDOnlyMediaRangeAndNoPathLeak(t *testing.T) {
	root, state := t.TempDir(), t.TempDir()
	videoRoot := filepath.Join(root, "MIJIA_RECORD_VIDEO", "2024010203")
	if err := os.MkdirAll(videoRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	content := []byte("0123456789")
	if err := os.WriteFile(filepath.Join(videoRoot, "04M05S_1704135845.mp4"), content, 0o600); err != nil {
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
	segment := archive.Segment{VideoPath: "2024010203/04M05S_1704135845.mp4", StartMS: 1704135845000, DurationMS: 60000, SizeBytes: int64(len(content)), MTimeNS: 1, VideoCodec: "hevc", AudioCodec: "pcm_alaw", ProbeStatus: "ready"}
	if err := db.ApplyScan(context.Background(), folder.ID, []archive.Segment{segment}, nil); err != nil {
		t.Fatal(err)
	}
	segments, _, err := db.Timeline(context.Background(), folder.ID, segment.StartMS, segment.StartMS+segment.DurationMS)
	if err != nil || len(segments) != 1 {
		t.Fatalf("segments=%d err=%v", len(segments), err)
	}
	tm, err := transcode.New(state, "ffmpeg", 1, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("Asia/Shanghai")
	hash, err := auth.HashPassword("test-password-123!")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsureAdmin(context.Background(), "admin", hash); err != nil {
		t.Fatal(err)
	}
	handler := (&Server{Location: loc, Store: db, Transcode: tm, Auth: auth.New(db, false), Library: media.Library{HostRoot: root, MountRoot: root}}).Handler()
	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewBufferString(`{"username":"admin","password":"test-password-123!"}`))
	login.Header.Set("Origin", "http://example.com")
	loginRR := httptest.NewRecorder()
	handler.ServeHTTP(loginRR, login)
	if loginRR.Code != http.StatusOK {
		t.Fatalf("login code=%d body=%s", loginRR.Code, loginRR.Body.String())
	}
	cookies := loginRR.Result().Cookies()
	authed := func(method, target string) *http.Request {
		req := httptest.NewRequest(method, target, nil)
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		return req
	}
	req := authed(http.MethodGet, "/api/v1/media/"+segments[0].PublicID+"/source")
	req.Header.Set("Range", "bytes=0-3")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusPartialContent || rr.Body.String() != "0123" {
		t.Fatalf("code=%d body=%q", rr.Code, rr.Body.String())
	}
	start := time.UnixMilli(segment.StartMS).UTC().Format(time.RFC3339)
	end := time.UnixMilli(segment.StartMS + segment.DurationMS).UTC().Format(time.RFC3339)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, authed(http.MethodGet, "/api/v1/timeline?folder="+folder.PublicID+"&start="+start+"&end="+end))
	body, _ := io.ReadAll(rr.Result().Body)
	if strings.Contains(string(body), "2024010203") || strings.Contains(string(body), root) {
		t.Fatalf("path leaked: %s", body)
	}
	for _, path := range []string{"/api/v1/media/not-an-id/source", "/api/v1/media/%2e%2e/source"} {
		rr = httptest.NewRecorder()
		handler.ServeHTTP(rr, authed(http.MethodGet, path))
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s returned %d", path, rr.Code)
		}
	}
}

func TestSafePathNASLayouts(t *testing.T) {
	for _, rel := range []string{"2026081007/12M56S_1786317176.mp4", "00_20260920162802_20260920163344.mp4"} {
		root := t.TempDir()
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("synthetic"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := (&Server{}).safePath(root, filepath.FromSlash(rel))
		if err != nil || got != path {
			t.Fatalf("got %q err %v", got, err)
		}
		if _, err := (&Server{}).safePath(root, filepath.Join("..", "outside.mp4")); err == nil {
			t.Fatal("accepted path escape")
		}
	}
}
