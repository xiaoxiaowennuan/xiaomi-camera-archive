package httpapi

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"mijia-archive/internal/auth"
	"mijia-archive/internal/media"
	"mijia-archive/internal/store"
	"mijia-archive/internal/transcode"
)

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type Server struct {
	WebRoot    string
	Location   *time.Location
	Store      *store.Store
	Transcode  *transcode.Manager
	Auth       *auth.Manager
	Library    media.Library
	ScanFolder func(store.FolderRecord)
}

func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /api/v1/auth/login", s.login)
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	m.HandleFunc("GET /api/v1/auth/me", s.protect(s.me, false))
	m.HandleFunc("POST /api/v1/auth/logout", s.protect(s.logout, false))
	m.HandleFunc("POST /api/v1/auth/password", s.protect(s.changePassword, false))
	m.HandleFunc("GET /api/v1/users", s.protect(s.users, true))
	m.HandleFunc("POST /api/v1/users", s.protect(s.createUser, true))
	m.HandleFunc("GET /api/v1/folders", s.protect(s.folders, false))
	m.HandleFunc("POST /api/v1/folders", s.protect(s.createFolder, true))
	m.HandleFunc("PUT /api/v1/folders/{id}", s.protect(s.updateFolder, true))
	m.HandleFunc("POST /api/v1/folders/{id}/scan", s.protect(s.scanFolder, true))
	m.HandleFunc("GET /api/v1/days", s.protect(s.days, false))
	m.HandleFunc("GET /api/v1/timeline", s.protect(s.timeline, false))
	m.HandleFunc("GET /api/v1/events", s.protect(s.events, false))
	m.HandleFunc("GET /api/v1/media/{id}/source", s.protect(s.source, false))
	m.HandleFunc("GET /api/v1/media/{id}/thumbnail", s.protect(s.thumbnail, false))
	m.HandleFunc("POST /api/v1/media/{id}/compat", s.protect(s.compatPost, false))
	m.HandleFunc("GET /api/v1/media/{id}/compat", s.protect(s.compatGet, false))
	if s.WebRoot != "" {
		files := http.FileServer(http.Dir(s.WebRoot))
		m.Handle("GET /assets/", files)
		m.HandleFunc("GET /login", s.serveIndex)
		m.HandleFunc("GET /", s.protect(s.serveIndex, false))
	}
	secured := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; media-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		m.ServeHTTP(w, r)
	})
	if s.Auth == nil {
		return secured
	}
	return s.Auth.Sessions.LoadAndSave(secured)
}

func (s *Server) serveIndex(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		problem(w, http.StatusNotFound, "not_found")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, filepath.Join(s.WebRoot, "index.html"))
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host == r.Host && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func (s *Server) protect(next http.HandlerFunc, adminOnly bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Auth == nil {
			problem(w, http.StatusServiceUnavailable, "authentication_unavailable")
			return
		}
		user, err := s.Auth.User(r.Context())
		if err != nil {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				problem(w, http.StatusUnauthorized, "authentication_required")
			} else {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}
			return
		}
		if adminOnly && user.Role != "admin" {
			problem(w, http.StatusForbidden, "admin_required")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			problem(w, http.StatusForbidden, "invalid_origin")
			return
		}
		next(w, r)
	}
}

func jsonOut(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func problem(w http.ResponseWriter, status int, code string) {
	jsonOut(w, status, map[string]string{"error": code})
}

func (s *Server) requestFolder(r *http.Request) (store.FolderRecord, error) {
	id := r.URL.Query().Get("folder")
	if !idPattern.MatchString(id) {
		return store.FolderRecord{}, os.ErrNotExist
	}
	return s.Store.Folder(r.Context(), id)
}

func (s *Server) days(w http.ResponseWriter, r *http.Request) {
	folder, err := s.requestFolder(r)
	if err != nil {
		problem(w, 404, "folder_not_found")
		return
	}
	from, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("from"), s.Location)
	if err != nil {
		problem(w, 400, "invalid_date")
		return
	}
	to, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("to"), s.Location)
	if err != nil || to.Before(from) || to.Sub(from) > 366*24*time.Hour {
		problem(w, 400, "invalid_date_range")
		return
	}
	days, err := s.Store.Days(r.Context(), folder.ID, from.UnixMilli(), to.Add(24*time.Hour).UnixMilli(), s.Location)
	if err != nil {
		problem(w, 500, "query_failed")
		return
	}
	jsonOut(w, 200, map[string]any{"days": days})
}

type publicSegment struct {
	ID           string  `json:"id"`
	StartMS      int64   `json:"startMs"`
	DurationMS   int64   `json:"durationMs"`
	VideoCodec   string  `json:"videoCodec"`
	AudioCodec   string  `json:"audioCodec"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	FPS          float64 `json:"fps"`
	HasThumbnail bool    `json:"hasThumbnail"`
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	folder, err := s.requestFolder(r)
	if err != nil {
		problem(w, 404, "folder_not_found")
		return
	}
	start, err := time.Parse(time.RFC3339, r.URL.Query().Get("start"))
	if err != nil {
		problem(w, 400, "invalid_time")
		return
	}
	end, err := time.Parse(time.RFC3339, r.URL.Query().Get("end"))
	if err != nil || !end.After(start) || end.Sub(start) > 48*time.Hour {
		problem(w, 400, "invalid_time_range")
		return
	}
	segments, events, err := s.Store.Timeline(r.Context(), folder.ID, start.UnixMilli(), end.UnixMilli())
	if err != nil {
		problem(w, 500, "query_failed")
		return
	}
	out := make([]publicSegment, 0, len(segments))
	for _, v := range segments {
		out = append(out, publicSegment{v.PublicID, v.StartMS, v.DurationMS, v.VideoCodec, v.AudioCodec, v.Width, v.Height, v.FPS, v.ThumbnailPath != ""})
	}
	jsonOut(w, 200, map[string]any{"segments": out, "events": events})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	folder, err := s.requestFolder(r)
	if err != nil {
		problem(w, 404, "folder_not_found")
		return
	}
	date, err := time.ParseInLocation("2006-01-02", r.URL.Query().Get("date"), s.Location)
	if err != nil {
		problem(w, 400, "invalid_date")
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			problem(w, 400, "invalid_limit")
			return
		}
		limit = n
	}
	cursor, _ := strconv.ParseInt(r.URL.Query().Get("cursor"), 10, 64)
	events, err := s.Store.Events(r.Context(), folder.ID, date.UnixMilli(), date.Add(24*time.Hour).UnixMilli(), cursor, limit)
	if err != nil {
		problem(w, 500, "query_failed")
		return
	}
	next := int64(0)
	if len(events) == limit {
		next = events[len(events)-1].StartMS
	}
	jsonOut(w, 200, map[string]any{"events": events, "nextCursor": next})
}

func (s *Server) record(r *http.Request) (store.SegmentRecord, error) {
	id := r.PathValue("id")
	if !idPattern.MatchString(id) {
		return store.SegmentRecord{}, os.ErrNotExist
	}
	return s.Store.Segment(r.Context(), id)
}
func (s *Server) safePath(mediaRoot, rel string) (string, error) {
	if filepath.IsAbs(rel) || filepath.Clean(rel) != rel {
		return "", os.ErrNotExist
	}
	root, err := filepath.Abs(filepath.Join(mediaRoot, "MIJIA_RECORD_VIDEO"))
	if err != nil {
		return "", err
	}
	path, err := filepath.Abs(filepath.Join(root, rel))
	if err != nil {
		return "", err
	}
	inside, err := filepath.Rel(root, path)
	if err != nil || inside == "." || inside == ".." || len(inside) > 2 && inside[:3] == ".."+string(os.PathSeparator) {
		return "", os.ErrNotExist
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", os.ErrNotExist
	}
	return path, nil
}
func (s *Server) serveRecord(w http.ResponseWriter, r *http.Request, thumbnail bool) {
	rec, err := s.record(r)
	if err != nil {
		problem(w, 404, "media_not_found")
		return
	}
	rel := rec.VideoPath
	if thumbnail {
		rel = rec.ThumbnailPath
		if rel == "" {
			problem(w, 404, "thumbnail_not_found")
			return
		}
	}
	path, err := s.safePath(rec.MountPath, rel)
	if err != nil {
		problem(w, 404, "media_not_found")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		problem(w, 404, "media_not_found")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		problem(w, 404, "media_not_found")
		return
	}
	if thumbnail {
		w.Header().Set("Content-Type", "image/jpeg")
	} else {
		w.Header().Set("Content-Type", "video/mp4")
	}
	http.ServeContent(w, r, "", info.ModTime(), f)
}
func (s *Server) source(w http.ResponseWriter, r *http.Request)    { s.serveRecord(w, r, false) }
func (s *Server) thumbnail(w http.ResponseWriter, r *http.Request) { s.serveRecord(w, r, true) }
func (s *Server) compatPost(w http.ResponseWriter, r *http.Request) {
	rec, err := s.record(r)
	if err != nil {
		problem(w, 404, "media_not_found")
		return
	}
	source, err := s.safePath(rec.MountPath, rec.VideoPath)
	if err != nil {
		problem(w, 404, "media_not_found")
		return
	}
	key := s.Transcode.Key(rec.PublicID, rec.SizeBytes, rec.MTimeNS)
	status := s.Transcode.Ensure(rec.PublicID, key, source)
	code := 202
	if status.State == "ready" {
		code = 200
	}
	jsonOut(w, code, status)
}
func (s *Server) compatGet(w http.ResponseWriter, r *http.Request) {
	rec, err := s.record(r)
	if err != nil {
		problem(w, 404, "media_not_found")
		return
	}
	key := s.Transcode.Key(rec.PublicID, rec.SizeBytes, rec.MTimeNS)
	path, err := s.Transcode.ReadyPath(key)
	if err != nil {
		problem(w, 409, "compat_not_ready")
		return
	}
	f, err := os.Open(path)
	if err != nil {
		problem(w, 409, "compat_not_ready")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		problem(w, 409, "compat_not_ready")
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	http.ServeContent(w, r, "", info.ModTime(), f)
}
