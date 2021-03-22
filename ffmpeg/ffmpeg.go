package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ffmpegBin string
var ffprobeBin string
var initGuard sync.Once

// Init sets the location of `ffmpeg` and `ffprobe` binary executable.
// No runner can be created before this function is called.
func Init(ffmpegBinLocation, ffprobeBinLocation string) {
	initGuard.Do(func() {
		ffmpegBin = ffmpegBinLocation
		ffprobeBin = ffprobeBinLocation
	})
}

type Runner struct {
	ffmpegBin  string // Location of `ffmpeg` binary executable
	ffprobeBin string // Location of `ffprobe` binary executable
	args       []string
	duration   time.Duration // Duration of current processing media
	timeout    time.Duration
}

// NewRunner creates a new Runner instance
func NewRunner(args ...string) (*Runner, error) {
	if ffmpegBin == "" {
		return nil, errors.New("ffmpeg not located, you should probably call Init first")
	}
	if ffprobeBin == "" {
		return nil, errors.New("ffprobe not located, you should probably call Init first")
	}

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

	return &Runner{args: fullArgs, ffmpegBin: ffmpegBin, ffprobeBin: ffprobeBin}, nil
}

// probSingleMediaDuration runs `ffprobe` command to get duration og given media file.
// It does not touch internal state of `r`.
func (r *Runner) ProbSingleMediaDuration(filePath string) (time.Duration, error) {
	var duration time.Duration

	timeout, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()

	probeProc := exec.CommandContext(timeout, r.ffprobeBin, "-show_entries", "format=duration", filePath)
	stdout, err := probeProc.StdoutPipe()
	if err != nil {
		return duration, err
	}

	if err := probeProc.Start(); err != nil {
		return duration, err
	}

	var durationStr string
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "duration=") {
				durationStr = strings.TrimSpace(strings.ReplaceAll(line, "duration=", ""))
				break
			}
		}

		io.Copy(ioutil.Discard, stdout)
	}()

	if err := probeProc.Wait(); err != nil {
		return duration, err
	}

	durationSec, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		return duration, err
	}

	duration = time.Duration(durationSec * float64(time.Second))
	return duration, nil
}

// ProbeMediaDuration runs `ffprobe` command to probe given list of media files.
// The result will be stored into `r` to be used as `total` of progress callback.
// This is because `ffmpeg` does not reliably output durations of all the media files it's processing, so we do a manual probe instead.
func (r *Runner) ProbeMediaDuration(listOfFiles ...string) error {
	var duration time.Duration

	for _, filePath := range listOfFiles {
		var mediaDuration time.Duration
		var err error

		if mediaDuration, err = r.ProbSingleMediaDuration(filePath); err != nil {
			return err
		}
		duration += mediaDuration
	}

	r.duration = duration
	return nil
}

// SetTimeout sets a timeout for given Runner instance
func (r *Runner) SetTimeout(timeout time.Duration) {
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

// Run runs the given Runner instance (spawns ffmpeg process).
// Pass a callback function to receive progress.
func (r *Runner) Run(progressCallback func(current, total int64)) error {
	if r.duration == 0 {
		return fmt.Errorf("total duration unknown, please call .ProbeMediaDuration first")
	}

	var proc *exec.Cmd
	if r.timeout == 0 {
		proc = exec.Command(r.ffmpegBin, r.args...)
	} else {
		timeout, cancel := context.WithTimeout(context.Background(), r.timeout)
		defer cancel()

		proc = exec.CommandContext(timeout, r.ffmpegBin, r.args...)
	}

	ffmpegStdout, err := proc.StdoutPipe()
	if err != nil {
		return err
	}

	if err := proc.Start(); err != nil {
		return err
	}

	go func() {
		stdoutScanner := bufio.NewScanner(ffmpegStdout)

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
					if processedDuration, err := parseDurationStr(timeFields[1]); err == nil && r.duration > 0 {
						progressCallback(processedDuration.Nanoseconds(), r.duration.Nanoseconds())
					}
				}
			}
		}
	}()

	if err = proc.Wait(); err == nil && progressCallback != nil {
		// Make sure 100% value is passed to progressCallback at least once.
		progressCallback(r.duration.Nanoseconds(), r.duration.Nanoseconds())
	}

	return err
}
