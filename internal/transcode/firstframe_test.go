package transcode

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestFirstFrameCachesAndNeverSeeksOrWritesSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell fake requires Unix")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source.mp4")
	os.WriteFile(source, []byte("unchanged"), 0400)
	fake := filepath.Join(root, "ffmpeg")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> \"$0.args\"\nfor last; do :; done\nprintf 'jpeg' > \"$last\"\n"
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	m, err := New(filepath.Join(root, "state"), fake, 1, 1024)
	if err != nil {
		t.Fatal(err)
	}
	key := m.Key("public-id", 9, 1)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			path, err := m.FirstFrame(context.Background(), key, source)
			if err != nil {
				t.Error(err)
				return
			}
			if !strings.HasPrefix(path, filepath.Join(root, "state")) {
				t.Error("cache escaped state")
			}
		}()
	}
	wg.Wait()
	args, _ := os.ReadFile(fake + ".args")
	if strings.Count(string(args), "-frames:v\n1\n") != 1 || strings.Contains(string(args), "-ss\n") {
		t.Fatalf("unexpected invocation: %s", args)
	}
	data, _ := os.ReadFile(source)
	if string(data) != "unchanged" {
		t.Fatal("source modified")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	m.thumbnailWorker <- struct{}{}
	_, err = m.FirstFrame(ctx, m.Key("other", 1, 1), source)
	<-m.thumbnailWorker
	if err == nil {
		t.Fatal("cancelled request should stop waiting")
	}
}
