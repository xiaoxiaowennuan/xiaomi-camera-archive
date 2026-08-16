package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLibraryRestrictsFoldersToConfiguredRoot(t *testing.T) {
	hostRoot := filepath.Join(string(filepath.Separator), "srv", "camera")
	mountRoot := t.TempDir()
	archive := filepath.Join(mountRoot, "family")
	if err := os.MkdirAll(filepath.Join(archive, "MIJIA_RECORD_VIDEO"), 0o700); err != nil {
		t.Fatal(err)
	}
	library := Library{HostRoot: hostRoot, MountRoot: mountRoot}
	got, err := library.IsArchiveFolder(filepath.Join(hostRoot, "family"))
	want, _ := filepath.EvalSymlinks(archive)
	if err != nil || got != want {
		t.Fatalf("got=%q err=%v", got, err)
	}
	for _, unsafe := range []string{"../etc", filepath.Join(hostRoot, "..", "private"), string(filepath.Separator)} {
		if _, err := library.Resolve(unsafe); err == nil {
			t.Fatalf("unsafe path accepted: %q", unsafe)
		}
	}
}
