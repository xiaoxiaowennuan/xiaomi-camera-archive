package transcode

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

// FirstFrame decodes the first video frame without seeking. Outputs never touch media.
func (m *Manager) FirstFrame(ctx context.Context, key, source string) (string, error) {
	root := filepath.Join(filepath.Dir(m.Root), "first-frames-v1")
	path := filepath.Join(root, key+".jpg")
	valid := func() bool {
		info, err := os.Lstat(path)
		return err == nil && info.Mode().IsRegular() && info.Size() > 0
	}
	if valid() {
		return path, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	select {
	case m.thumbnailWorker <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-m.thumbnailWorker }()
	if valid() {
		return path, nil
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(root, "frame-*.jpg")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	args := []string{"-nostdin", "-v", "error", "-threads", "1", "-i", source, "-map", "0:v:0", "-frames:v", "1", "-vf", "scale=480:-2", "-threads", "1", "-q:v", "4", "-f", "image2", "-y", tmpPath}
	if err := exec.CommandContext(ctx, m.FFmpeg, args...).Run(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", err
	}
	// Keep derived covers bounded independently of compatibility videos.
	entries, _ := os.ReadDir(root)
	if len(entries) > 12000 {
		var covers []os.FileInfo
		for _, entry := range entries {
			if entry.Name() == key+".jpg" || filepath.Ext(entry.Name()) != ".jpg" {
				continue
			}
			info, err := entry.Info()
			if err == nil && info.Mode().IsRegular() {
				covers = append(covers, info)
			}
		}
		sort.Slice(covers, func(i, j int) bool { return covers[i].ModTime().Before(covers[j].ModTime()) })
		for i := 0; i < len(covers)-11999; i++ {
			_ = os.Remove(filepath.Join(root, covers[i].Name()))
		}
	}
	return path, nil
}
