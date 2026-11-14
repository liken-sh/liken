package main

// titlestool.go runs the titles tool, the Rust binary in titles/ that the
// titles image carries. `titles read` decodes the end of a video, finds the
// text in a frame of each second, reads the text of some of them, and writes
// the reading beside the video. The worker owns the ledger, so the tool
// writes no attempt, and every failure of the tool comes back here with the
// tool's own words.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// The binary, a variable so a test runs a stand-in in its place.
var titlesBinary = "/titles"

// How long one read may run. The tool decodes at most the window, and the
// timeout bounds only a decode that hangs, so it allows a software decode
// of a 4K film's window on a slow node with room to spare.
const titlesReadTimeout = time.Hour

// The most of the tool's stderr an attempt keeps. The tool's error is its
// last line, and the lines before it say what it did, so the end of the text
// is the part to keep.
const titlesReasonLimit = 4096

// One run of the tool that failed: the exit, and the tool's stderr word for
// word. The error's text ends with the last line of stderr, which is the
// tool's own error.
type titlesFailure struct {
	err    error
	stderr string
}

func (f *titlesFailure) Error() string {
	text := strings.TrimSpace(f.stderr)
	if at := strings.LastIndexByte(text, '\n'); at >= 0 {
		text = text[at+1:]
	}
	return fmt.Sprintf("titles read: %v: %s", f.err, text)
}

// The text an attempt records for a failure: the tool's stderr, or the error
// itself where the tool did not run or ran and said nothing wrong.
func titlesReason(err error) string {
	var failure *titlesFailure
	if !errors.As(err, &failure) || strings.TrimSpace(failure.stderr) == "" {
		return err.Error()
	}
	text := strings.TrimSpace(failure.stderr)
	if len(text) > titlesReasonLimit {
		text = text[len(text)-titlesReasonLimit:]
	}
	return text
}

// One run of the tool, and a failure that carries its stderr.
func runTitles(ctx context.Context, args ...string) error {
	timed, cancel := context.WithTimeout(ctx, titlesReadTimeout)
	defer cancel()
	var stderr bytes.Buffer
	command := exec.CommandContext(timed, titlesBinary, args...)
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return &titlesFailure{err: err, stderr: stderr.String()}
	}
	return nil
}

// The arguments of read. The text models run on two threads of the CPU,
// which plan 96 measured to be as fast as an Intel GPU at the precision the
// detector needs. Two decode threads bound ffmpeg's memory, because ffmpeg
// otherwise starts a thread per core of the node, and each thread holds its
// own frames. With a render node, ffmpeg decodes and scales on it, and the
// tool decodes a video the node refuses again in software by itself.
func readArgs(video string, window time.Duration, hardware bool) []string {
	args := []string{"read", "--window", strconv.Itoa(int(window / time.Second)),
		"--threads", "2", "--decode-threads", "2"}
	if hardware {
		args = append(args, "--hwaccel", "vaapi")
	}
	return append(args, video)
}
