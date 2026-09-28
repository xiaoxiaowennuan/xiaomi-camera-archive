package media

import (
	"errors"
	"mijia-archive/internal/archive"
	"os"
	"path/filepath"
	"strings"
)

type Library struct {
	HostRoot  string
	MountRoot string
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func (l Library) Resolve(serverPath string) (string, error) {
	hostRoot, err := filepath.Abs(l.HostRoot)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(serverPath) || filepath.Clean(serverPath) != serverPath {
		return "", errors.New("server path must be absolute and clean")
	}
	hostPath, err := filepath.Abs(serverPath)
	if err != nil || !within(hostRoot, hostPath) {
		return "", errors.New("server path is outside the configured media library")
	}
	rel, err := filepath.Rel(hostRoot, hostPath)
	if err != nil {
		return "", err
	}
	mountRoot, err := filepath.Abs(l.MountRoot)
	if err != nil {
		return "", err
	}
	mountPath := filepath.Join(mountRoot, rel)
	resolvedRoot, err := filepath.EvalSymlinks(mountRoot)
	if err != nil {
		return "", errors.New("media library is not mounted")
	}
	resolvedPath, err := filepath.EvalSymlinks(mountPath)
	if err != nil || !within(resolvedRoot, resolvedPath) {
		return "", errors.New("folder is unavailable in the mounted media library")
	}
	info, err := os.Stat(resolvedPath)
	if err != nil || !info.IsDir() {
		return "", errors.New("folder is not a directory")
	}
	return resolvedPath, nil
}

func (l Library) IsArchiveFolder(serverPath string) (string, error) {
	mountPath, err := l.Resolve(serverPath)
	if err != nil {
		return "", err
	}
	if !archive.HasVideoLayout(mountPath) {
		return "", errors.New("recognized video layout is missing")
	}
	return mountPath, nil
}
