package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"

	"mijia-archive/internal/archive"
)

type FFProbe struct{ Path string }

type output struct {
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		AvgRate   string `json:"avg_frame_rate"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func (p FFProbe) Probe(ctx context.Context, path string) (archive.ProbeResult, error) {
	if p.Path == "" {
		return archive.ProbeResult{}, errors.New("ffprobe path is required")
	}
	cmd := exec.CommandContext(ctx, p.Path, "-v", "error", "-print_format", "json", "-show_streams", "-show_format", path)
	b, err := cmd.Output()
	if err != nil {
		return archive.ProbeResult{}, errors.New("ffprobe failed")
	}
	var out output
	if err := json.Unmarshal(b, &out); err != nil {
		return archive.ProbeResult{}, errors.New("invalid ffprobe output")
	}
	duration, err := strconv.ParseFloat(out.Format.Duration, 64)
	if err != nil || duration <= 0 {
		return archive.ProbeResult{}, errors.New("invalid media duration")
	}
	result := archive.ProbeResult{DurationMS: int64(duration*1000 + 0.5)}
	for _, stream := range out.Streams {
		switch stream.CodecType {
		case "video":
			result.VideoCodec, result.Width, result.Height = stream.CodecName, stream.Width, stream.Height
			var n, d float64
			if _, err := fmt.Sscanf(stream.AvgRate, "%f/%f", &n, &d); err == nil && d != 0 {
				result.FPS = n / d
			}
		case "audio":
			result.AudioCodec = stream.CodecName
		}
	}
	if result.VideoCodec == "" {
		return archive.ProbeResult{}, errors.New("video stream missing")
	}
	return result, nil
}
