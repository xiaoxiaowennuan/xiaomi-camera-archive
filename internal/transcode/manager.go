package transcode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

type Status struct {
	State        string `json:"state"`
	RetryAfterMS int    `json:"retryAfterMs,omitempty"`
}
type Manager struct {
	Root, FFmpeg string
	mu           sync.Mutex
	jobs         map[string]string
	worker       chan struct{}
	maxBytes     int64
}

func New(root, ffmpeg string, workers int, maxBytes int64) (*Manager, error) {
	if workers < 1 {
		workers = 1
	}
	cache := filepath.Join(root, "cache", "compat-v1")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return nil, err
	}
	return &Manager{Root: cache, FFmpeg: ffmpeg, jobs: map[string]string{}, worker: make(chan struct{}, workers), maxBytes: maxBytes}, nil
}
func (m *Manager) Key(id string, size, mtime int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("compat-v1:%s:%d:%d", id, size, mtime)))
	return hex.EncodeToString(sum[:])
}
func (m *Manager) Path(key string) string { return filepath.Join(m.Root, key+".mp4") }
func (m *Manager) Ensure(id, key, source string) Status {
	path := m.Path(key)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return Status{State: "ready"}
	}
	m.mu.Lock()
	if state := m.jobs[key]; state != "" {
		m.mu.Unlock()
		return Status{State: state, RetryAfterMS: 1000}
	}
	m.jobs[key] = "pending"
	m.mu.Unlock()
	go m.run(key, source, path)
	return Status{State: "pending", RetryAfterMS: 1000}
}
func (m *Manager) run(key, source, dest string) {
	m.worker <- struct{}{}
	defer func() { <-m.worker }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	tmp, err := os.CreateTemp(m.Root, key+"-*.tmp")
	if err != nil {
		m.set(key, "failed")
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	args := []string{"-nostdin", "-v", "error", "-i", source, "-map", "0:v:0", "-map", "0:a:0?", "-vf", "scale='min(1920,iw)':-2", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "veryfast", "-crf", "23", "-g", "60", "-c:a", "aac", "-ac", "1", "-b:a", "64k", "-movflags", "+faststart", "-f", "mp4", "-y", tmpPath}
	if err = exec.CommandContext(ctx, m.FFmpeg, args...).Run(); err == nil {
		err = os.Rename(tmpPath, dest)
	}
	if err != nil {
		m.set(key, "failed")
	} else {
		m.prune(dest)
		m.set(key, "ready")
	}
}
func (m *Manager) set(key, state string) { m.mu.Lock(); m.jobs[key] = state; m.mu.Unlock() }
func (m *Manager) ReadyPath(key string) (string, error) {
	path := m.Path(key)
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("cache entry is not regular")
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return path, nil
}

func (m *Manager) prune(keep string) {
	if m.maxBytes <= 0 {
		return
	}
	entries, err := os.ReadDir(m.Root)
	if err != nil {
		return
	}
	type item struct {
		path string
		size int64
		mod  time.Time
	}
	var files []item
	var total int64
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".mp4" {
			continue
		}
		info, err := e.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, item{filepath.Join(m.Root, e.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })
	for _, f := range files {
		if total <= m.maxBytes {
			break
		}
		if f.path == keep {
			continue
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}
