package main

import (
	"bufio"
	"context"
	"io"
	"io/ioutil"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type FFmpegRunner struct {
	bin      string // Location of `ffmpeg` binary executable
	args     []string
	duration time.Duration // Duration of current processing media
	timeout  time.Duration
}

// NewFFmpegRunner creates a new FFmpegRunner instance
func NewFFmpegRunner(args ...string) *FFmpegRunner {
	fullArgs := []string{"-progress", "-", "-nostats"}

	var progValueIndex int
	for i, a := range args {
		if a == "-nostats" {
			continue
		}
		if a == "-progress" {
			progValueIndex = i + 1
			continue
		}
		if progValueIndex > 0 && i == progValueIndex {
			continue
		}

		fullArgs = append(fullArgs, a)
	}

	return &FFmpegRunner{args: fullArgs, bin: ffmpegBin}
}

// SetTimeout sets a timeout for given FFmpegRunner instance
func (r *FFmpegRunner) SetTimeout(timeout time.Duration) {
	r.timeout = timeout
}

// parseDurationStr parses ffmpeg duration string like `01:02:02.61` into a `time.Duration`. Might fail with an error.
func parseDurationStr(str string) (time.Duration, error) {
	durationFields := strings.Split(strings.TrimSpace(str), ":")
	var durationNs, h, m int64
	var s float64
	var err error
	var duration time.Duration

	if h, err = strconv.ParseInt(durationFields[0], 10, 64); err != nil {
		return duration, err
	}
	durationNs += int64(time.Hour) * h

	if m, err = strconv.ParseInt(durationFields[1], 10, 64); err != nil {
		return duration, err
	}
	durationNs += int64(time.Minute) * m

	if s, err = strconv.ParseFloat(durationFields[2], 64); err != nil {
		return duration, err
	}
	durationNs += int64(float64(time.Second) * s)

	return time.Duration(durationNs), err
}

// Run runs the given FFmpegRunner instance (spawns ffmpeg process).
// Pass a callback function to receive progress.
func (r *FFmpegRunner) Run(progressCallback func(current, total int64)) error {
	var proc *exec.Cmd
	if r.timeout == 0 {
		proc = exec.Command(r.bin, r.args...)
	} else {
		timeout, cancel := context.WithTimeout(context.Background(), r.timeout)
		defer cancel()

		proc = exec.CommandContext(timeout, r.bin, r.args...)
	}

	ffmpegStdout, err := proc.StdoutPipe()
	if err != nil {
		return err
	}

	ffmpegStderr, err := proc.StderrPipe()
	if err != nil {
		return err
	}

	if err := proc.Start(); err != nil {
		return err
	}

	go func() {
		// Process stderr, calculate progress, and pass it to `progressCallback`
		//ticker := time.NewTicker(time.Millisecond * 100)
		stderrScanner := bufio.NewScanner(ffmpegStderr)
		stdoutScanner := bufio.NewScanner(ffmpegStdout)

		for stderrScanner.Scan() {
			line := stderrScanner.Text()
			// Set total duration of current media file
			// Sample: `  Duration: 01:02:02.68, start: 0.000000, bitrate: 8316 kb/s`
			if strings.Contains(line, "Duration:") && r.duration == 0 {
				durationStr := strings.Split(strings.TrimSpace(strings.Replace(line, "Duration:", "", 1)), ",")[0]
				if duration, err := parseDurationStr(durationStr); err == nil {
					r.duration = duration
					break
				}
			}
		}

		defer io.Copy(ioutil.Discard, ffmpegStderr)

		for stdoutScanner.Scan() {
			line := stdoutScanner.Text()
			// Get current progress
			// Sample: `out_time=00:29:13.066011`
			if progressCallback == nil {
				continue
			}

			if strings.Contains(line, "out_time=") {
				timeFields := strings.FieldsFunc(strings.TrimSpace(line), func(r rune) bool {
					return r == '='
				})
				if len(timeFields) == 2 {
					if duration, err := parseDurationStr(timeFields[1]); err == nil {
						progressCallback(duration.Nanoseconds(), r.duration.Nanoseconds())
					}
				}
			}
		}

	}()

	return proc.Wait()
}
