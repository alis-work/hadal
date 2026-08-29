package transcription

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type DurationProber interface {
	Probe(context.Context, string) (time.Duration, error)
}

type FFProbe struct{}

func (FFProbe) Probe(ctx context.Context, path string) (time.Duration, error) {
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nokey=1:noprint_wrappers=1", path).Output()
	if err != nil {
		return 0, fmt.Errorf("probe audio duration: %w", err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(string(output)), 64)
	if err != nil || seconds <= 0 {
		return 0, fmt.Errorf("probe audio duration: invalid result")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}
