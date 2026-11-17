package video

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	MaxBytes      = 300_000_000
	mimeType      = "video/mp4"
	downloadLimit = 5 * time.Minute
	commandLimit  = 2 * time.Minute
)

const formatSelector = "hd/sd/bv*[vcodec^=avc1]+ba[acodec^=mp4a]/b[ext=mp4]/b"

type Video struct {
	Path     string
	Size     int64
	Width    int64
	Height   int64
	Duration time.Duration
	MimeType string

	dir string
}

func (v *Video) Cleanup() {
	if v == nil || v.dir == "" {
		return
	}
	_ = os.RemoveAll(v.dir)
}

func Download(ctx context.Context, rawURL string) (*Video, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("invalid HTTP(S) video URL")
	}

	ctx, cancel := context.WithTimeout(ctx, downloadLimit)
	defer cancel()

	dir, err := os.MkdirTemp("", "facebook-to-bluesky-video-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()

	output := filepath.Join(dir, "video.%(ext)s")
	args := []string{
		"--no-playlist",
		"--no-warnings",
		"--no-progress",
		"--no-cache-dir",
		"--socket-timeout", "20",
		"--retries", "3",
		"--merge-output-format", "mp4",
		"--remux-video", "mp4",
		"-f", formatSelector,
		"-o", output,
		parsed.String(),
	}
	if err := run(ctx, "yt-dlp", args...); err != nil {
		return nil, err
	}

	matches, err := filepath.Glob(filepath.Join(dir, "video.*"))
	if err != nil || len(matches) == 0 {
		return nil, fmt.Errorf("yt-dlp did not produce an output file")
	}
	path := matches[0]

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat video: %w", err)
	}
	if info.Size() <= 0 {
		return nil, fmt.Errorf("video is empty")
	}
	if info.Size() > MaxBytes {
		return nil, fmt.Errorf("video exceeds %d MB", MaxBytes/1_000_000)
	}

	meta, err := probe(ctx, path)
	if err != nil {
		return nil, err
	}

	if meta.needsTranscode() {
		converted := filepath.Join(dir, "converted.mp4")
		if err := transcode(ctx, path, converted, meta.audio); err != nil {
			return nil, err
		}

		path = converted
		if info, err = os.Stat(path); err != nil {
			return nil, fmt.Errorf("stat converted video: %w", err)
		}
		if info.Size() > MaxBytes {
			return nil, fmt.Errorf("video exceeds %d MB after conversion", MaxBytes/1_000_000)
		}
		if meta, err = probe(ctx, path); err != nil {
			return nil, err
		}
	}

	cleanup = false

	return &Video{
		Path:     path,
		Size:     info.Size(),
		Width:    meta.width,
		Height:   meta.height,
		Duration: meta.duration,
		MimeType: mimeType,
		dir:      dir,
	}, nil
}

type metadata struct {
	width      int64
	height     int64
	duration   time.Duration
	videoCodec string
	audioCodec string
	audio      bool
}

func (m *metadata) needsTranscode() bool {
	return m.videoCodec != "h264" || (m.audio && m.audioCodec != "aac")
}

type probeOutput struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int64  `json:"width"`
		Height    int64  `json:"height"`
	} `json:"streams"`
}

func probe(ctx context.Context, path string) (*metadata, error) {
	ctx, cancel := context.WithTimeout(ctx, commandLimit)
	defer cancel()

	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffprobe: %w: %s", err, tail(stderr.String()))
	}

	var out probeOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("decode ffprobe output: %w", err)
	}

	meta := &metadata{}
	for _, stream := range out.Streams {
		switch stream.CodecType {
		case "video":
			if meta.width == 0 {
				meta.width = stream.Width
				meta.height = stream.Height
				meta.videoCodec = stream.CodecName
			}
		case "audio":
			if !meta.audio {
				meta.audio = true
				meta.audioCodec = stream.CodecName
			}
		}
	}
	if meta.width <= 0 || meta.height <= 0 {
		return nil, fmt.Errorf("ffprobe found no video stream")
	}

	if seconds, err := strconv.ParseFloat(out.Format.Duration, 64); err == nil && seconds > 0 {
		meta.duration = time.Duration(seconds * float64(time.Second))
	}

	return meta, nil
}

func transcode(ctx context.Context, input, output string, hasAudio bool) error {
	ctx, cancel := context.WithTimeout(ctx, downloadLimit)
	defer cancel()

	args := []string{
		"-y",
		"-hide_banner",
		"-loglevel", "error",
		"-i", input,
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-pix_fmt", "yuv420p",
	}
	if hasAudio {
		args = append(args, "-c:a", "aac", "-b:a", "128k")
	} else {
		args = append(args, "-an")
	}
	args = append(args, "-movflags", "+faststart", output)

	return run(ctx, "ffmpeg", args...)
}

func run(ctx context.Context, name string, args ...string) error {
	if _, err := exec.LookPath(name); err != nil {
		return fmt.Errorf("%s not installed", name)
	}

	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, tail(stderr.String()))
	}

	return nil
}

func tail(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) <= 500 {
		return raw
	}
	return raw[len(raw)-500:]
}
