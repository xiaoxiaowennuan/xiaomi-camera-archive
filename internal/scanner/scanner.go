package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mijia-archive/internal/archive"
	"mijia-archive/internal/store"
)

type Prober interface {
	Probe(context.Context, string) (archive.ProbeResult, error)
}

type Scanner struct {
	FolderID  int64
	MediaRoot string
	Location  *time.Location
	Store     *store.Store
	Prober    Prober
}
type Summary struct{ Segments, Events, Probed, Reused, Damaged int }

func validateRoots(mediaRoot, statePath string) error {
	m, err := filepath.Abs(mediaRoot)
	if err != nil {
		return err
	}
	s, err := filepath.Abs(statePath)
	if err != nil {
		return err
	}
	if m == s || strings.HasPrefix(m+string(os.PathSeparator), s+string(os.PathSeparator)) || strings.HasPrefix(s+string(os.PathSeparator), m+string(os.PathSeparator)) {
		return errors.New("media and state paths must be separate")
	}
	return nil
}

func (s *Scanner) Scan(ctx context.Context, statePath string) (Summary, error) {
	var summary Summary
	if s.FolderID < 1 || s.Location == nil || s.Store == nil || s.Prober == nil {
		return summary, errors.New("scanner is not configured")
	}
	if err := validateRoots(s.MediaRoot, statePath); err != nil {
		return summary, err
	}
	videoRoot := filepath.Join(s.MediaRoot, "MIJIA_RECORD_VIDEO")
	hours, err := os.ReadDir(videoRoot)
	if err != nil {
		return summary, errors.New("video root is not readable")
	}
	var segments []archive.Segment
	for _, hour := range hours {
		if hour.Type()&os.ModeSymlink != 0 || !hour.IsDir() || len(hour.Name()) != 10 {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(videoRoot, hour.Name()))
		if err != nil {
			return summary, errors.New("video directory is not readable")
		}
		for _, entry := range entries {
			if !archive.IsRegularNoSymlink(entry) || !strings.HasSuffix(entry.Name(), ".mp4") {
				continue
			}
			rel := filepath.Join(hour.Name(), entry.Name())
			started, err := archive.ParseVideoPath(rel, s.Location)
			if err != nil {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			seg := archive.Segment{VideoPath: rel, StartMS: started.UnixMilli(), SizeBytes: info.Size(), MTimeNS: info.ModTime().UnixNano(), ProbeStatus: "ready"}
			jpeg := strings.TrimSuffix(rel, ".mp4") + ".jpeg"
			if ji, err := os.Lstat(filepath.Join(videoRoot, jpeg)); err == nil && ji.Mode().IsRegular() {
				seg.ThumbnailPath = jpeg
			}
			existing, ok, err := s.Store.Existing(ctx, s.FolderID, rel)
			if err != nil {
				return summary, err
			}
			if ok && existing.SizeBytes == seg.SizeBytes && existing.MTimeNS == seg.MTimeNS {
				seg.DurationMS, seg.VideoCodec, seg.AudioCodec, seg.Width, seg.Height, seg.FPS = existing.Probe.DurationMS, existing.Probe.VideoCodec, existing.Probe.AudioCodec, existing.Probe.Width, existing.Probe.Height, existing.Probe.FPS
				seg.ProbeStatus, seg.ProbeError = existing.Status, existing.ProbeError
				summary.Reused++
			} else {
				probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
				p, err := s.Prober.Probe(probeCtx, filepath.Join(videoRoot, rel))
				cancel()
				summary.Probed++
				if err != nil {
					seg.ProbeStatus, seg.ProbeError, seg.DurationMS = "damaged", "media probe failed", 60_000
					summary.Damaged++
				} else {
					seg.DurationMS, seg.VideoCodec, seg.AudioCodec, seg.Width, seg.Height, seg.FPS = p.DurationMS, p.VideoCodec, p.AudioCodec, p.Width, p.Height, p.FPS
				}
			}
			segments = append(segments, seg)
		}
	}
	sort.Slice(segments, func(i, j int) bool { return segments[i].StartMS < segments[j].StartMS })
	events, err := readEvents(filepath.Join(s.MediaRoot, "MIJIA_RECORD_MOTION"))
	if err != nil {
		return summary, err
	}
	if err := s.Store.ApplyScan(ctx, s.FolderID, segments, events); err != nil {
		return summary, err
	}
	summary.Segments, summary.Events = len(segments), len(events)
	return summary, nil
}

func readEvents(root string) ([]archive.Event, error) {
	hours, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("motion root is not readable")
	}
	merged := make(map[int64]archive.Event)
	for _, hour := range hours {
		if hour.Type()&os.ModeSymlink != 0 || !hour.IsDir() {
			continue
		}
		path := filepath.Join(root, hour.Name(), "motion_record_msg")
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open motion metadata: %w", err)
		}
		events, parseErr := archive.ParseMotion(f)
		f.Close()
		if parseErr != nil {
			return nil, errors.New("invalid motion metadata")
		}
		for _, event := range events {
			old := merged[event.StartMS]
			old.StartMS = event.StartMS
			old.RawRecordCount += event.RawRecordCount
			old.HasUnknownFlag = old.HasUnknownFlag || event.HasUnknownFlag
			merged[event.StartMS] = old
		}
	}
	result := make([]archive.Event, 0, len(merged))
	for _, event := range merged {
		result = append(result, event)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].StartMS < result[j].StartMS })
	return result, nil
}
