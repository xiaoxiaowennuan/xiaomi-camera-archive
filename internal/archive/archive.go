package archive

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	flatPattern  = regexp.MustCompile(`^[0-9]{2}_([0-9]{14})_([0-9]{14})\.mp4$`)
	hourPattern  = regexp.MustCompile(`^[0-9]{10}$`)
	videoPattern = regexp.MustCompile(`^([0-9]{2})M([0-9]{2})S_([0-9]{10})\.mp4$`)
)

type ProbeResult struct {
	DurationMS int64
	VideoCodec string
	AudioCodec string
	Width      int
	Height     int
	FPS        float64
}

type Segment struct {
	PublicID      string
	VideoPath     string
	ThumbnailPath string
	StartMS       int64
	DurationMS    int64
	SizeBytes     int64
	MTimeNS       int64
	VideoCodec    string
	AudioCodec    string
	Width         int
	Height        int
	FPS           float64
	ProbeStatus   string
	ProbeError    string
}

type Event struct {
	StartMS        int64
	RawRecordCount int
	HasUnknownFlag bool
}

func ParseVideoPath(rel string, loc *time.Location) (time.Time, error) {
	if filepath.IsAbs(rel) || filepath.Clean(rel) != rel {
		return time.Time{}, errors.New("invalid relative path")
	}
	if loc == nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return time.Time{}, errors.New("invalid relative path or timezone")
	}
	dir, name := filepath.Split(rel)
	if m := flatPattern.FindStringSubmatch(name); m != nil && dir == "" {
		start, err := time.ParseInLocation("20060102150405", m[1], loc)
		if err != nil {
			return time.Time{}, errors.New("invalid start time")
		}
		end, err := time.ParseInLocation("20060102150405", m[2], loc)
		if err != nil || !end.After(start) {
			return time.Time{}, errors.New("invalid end time")
		}
		return start, nil
	}
	dir = filepath.Base(filepath.Clean(dir))
	if !hourPattern.MatchString(dir) {
		return time.Time{}, errors.New("invalid hour directory")
	}
	m := videoPattern.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}, errors.New("invalid video filename")
	}
	epoch, err := strconv.ParseInt(m[3], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse epoch: %w", err)
	}
	t := time.Unix(epoch, 0).In(loc)
	if t.Format("2006010215") != dir || t.Format("04") != m[1] || t.Format("05") != m[2] {
		return time.Time{}, errors.New("filename time fields disagree")
	}
	return t, nil
}

func ParseMotion(r io.Reader) ([]Event, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if len(b) < 128 || string(b[:8]) != "IMIEVENT" || (len(b)-128)%32 != 0 {
		return nil, errors.New("invalid motion record layout")
	}
	byEpoch := make(map[uint32]*Event)
	for off := 128; off < len(b); off += 32 {
		epoch := binary.LittleEndian.Uint32(b[off : off+4])
		flag := binary.LittleEndian.Uint32(b[off+8 : off+12])
		if epoch == 0 || flag > 1 {
			return nil, errors.New("invalid motion record fields")
		}
		e := byEpoch[epoch]
		if e == nil {
			e = &Event{StartMS: int64(epoch) * 1000}
			byEpoch[epoch] = e
		}
		e.RawRecordCount++
		e.HasUnknownFlag = e.HasUnknownFlag || flag == 1
	}
	events := make([]Event, 0, len(byEpoch))
	for _, e := range byEpoch {
		events = append(events, *e)
	}
	return events, nil
}

func IsRegularNoSymlink(entry os.DirEntry) bool {
	if entry.Type()&os.ModeSymlink != 0 {
		return false
	}
	info, err := entry.Info()
	return err == nil && info.Mode().IsRegular()
}

// VideoRoot accepts the original wrapper or camera directories written by NAS backup.
// A present but invalid wrapper must never silently switch the indexed root.
func VideoRoot(root string) (string, error) {
	wrapped := filepath.Join(root, "MIJIA_RECORD_VIDEO")
	info, err := os.Lstat(wrapped)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("invalid video root")
		}
		return wrapped, nil
	}
	if !os.IsNotExist(err) {
		return "", errors.New("video root is not readable")
	}
	info, err = os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("video root is not readable")
	}
	return root, nil
}

func HasVideoLayout(root string) bool {
	videoRoot, err := VideoRoot(root)
	if err != nil {
		return false
	}
	if videoRoot != root {
		return true
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if entry.IsDir() && hourPattern.MatchString(entry.Name()) {
			return true
		}
		if IsRegularNoSymlink(entry) && flatPattern.MatchString(entry.Name()) {
			return true
		}
	}
	return false
}
