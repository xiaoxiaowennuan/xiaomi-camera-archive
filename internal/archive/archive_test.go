package archive

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

func TestParseVideoPath(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	got, err := ParseVideoPath("2024010203/04M05S_1704135845.mp4", loc)
	if err != nil || got.Unix() != 1704135845 {
		t.Fatalf("got %v, %v", got, err)
	}
	for _, bad := range []string{"../x.mp4", "/tmp/x.mp4", "2024010203/05M05S_1704135845.mp4", "2024010203/x.mp4"} {
		if _, err := ParseVideoPath(bad, loc); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestParseMotionDeduplicatesAndPreservesFlag(t *testing.T) {
	b := make([]byte, 128+3*32)
	copy(b, "IMIEVENT")
	for i, rec := range []struct{ epoch, flag uint32 }{{10, 0}, {10, 1}, {20, 1}} {
		off := 128 + i*32
		binary.LittleEndian.PutUint32(b[off:], rec.epoch)
		binary.LittleEndian.PutUint32(b[off+8:], rec.flag)
	}
	events, err := ParseMotion(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d events", len(events))
	}
	for _, e := range events {
		if e.StartMS == 10_000 && (e.RawRecordCount != 2 || !e.HasUnknownFlag) {
			t.Fatalf("bad merged event: %+v", e)
		}
	}
}
