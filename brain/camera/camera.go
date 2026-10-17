// Package camera takes a single photo from a V4L2 webcam with ffmpeg, kept
// in memory only: nothing is written to disk.
package camera

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Webcam is a V4L2 device such as /dev/video0.
type Webcam struct {
	Device  string        // default /dev/video0
	Size    string        // capture size, default 960x720 (4:3)
	Warmup  int           // frames skipped while exposure settles, default 15 (~0.5 s)
	Timeout time.Duration // default 10 s
}

// Snap returns one JPEG frame.
func (w Webcam) Snap(ctx context.Context) ([]byte, error) {
	dev, size, warm, timeout := w.Device, w.Size, w.Warmup, w.Timeout
	if dev == "" {
		dev = "/dev/video0"
	}
	if size == "" {
		size = "960x720"
	}
	if warm <= 0 {
		warm = 15
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "v4l2", "-input_format", "mjpeg", "-video_size", size, "-i", dev,
		"-vf", fmt.Sprintf("select=gte(n\\,%d)", warm), "-fps_mode", "passthrough", "-frames:v", "1",
		"-q:v", "4", "-f", "image2pipe", "-c:v", "mjpeg", "pipe:1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("camera: no picture within %s", timeout)
		}
		return nil, fmt.Errorf("camera: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	if out.Len() < 2 || out.Bytes()[0] != 0xFF || out.Bytes()[1] != 0xD8 {
		return nil, errors.New("camera: no JPEG came back")
	}
	return out.Bytes(), nil
}
