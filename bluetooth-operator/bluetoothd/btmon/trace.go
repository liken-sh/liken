package main

// The supervisor: one btmon while the btmon file says true, and none
// while it does not.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

// restartDelay is how long the supervisor waits before it starts a
// btmon that exited on its own, or one that failed to start. A btmon
// that fails at once, such as one with no NET_RAW, would otherwise
// start again in a tight loop and fill the log.
const restartDelay = 5 * time.Second

// trace is one running btmon. The supervisor starts it through a
// startFunc, so a test runs a fake in its place.
type trace interface {
	// done closes when the process has exited.
	done() <-chan struct{}

	// stop ends the process and returns when it has exited.
	stop()
}

type startFunc func() (trace, error)

// supervisor runs one trace while the file at setting says true.
type supervisor struct {
	setting string
	start   startFunc
	delay   time.Duration
}

// run reads the setting once, then again on each change, and starts
// or stops the trace to agree with it. It returns nil when ctx ends,
// and the watch's error when the watch fails. It stops the trace
// before it returns either way.
func (s *supervisor) run(ctx context.Context, changes <-chan struct{}, failed <-chan error) error {
	var running trace
	// retry fires when the delay after an exit or a failed start ends.
	// It is nil while no restart waits.
	var retry <-chan time.Time
	stop := func() {
		if running != nil {
			fmt.Fprintln(os.Stderr, "start-btmon: stopping btmon")
			running.stop()
			running = nil
		}
	}
	defer stop()

	apply := func() {
		if !readSetting(s.setting) {
			stop()
			retry = nil
			return
		}
		if running != nil || retry != nil {
			return
		}
		fmt.Fprintln(os.Stderr, "start-btmon: starting btmon")
		child, err := s.start()
		if err != nil {
			fmt.Fprintf(os.Stderr, "start-btmon: starting btmon: %v; trying again in %s\n", err, s.delay)
			retry = time.After(s.delay)
			return
		}
		running = child
	}

	apply()
	for {
		// A nil channel never receives, so the case waits only while a
		// trace runs.
		var exited <-chan struct{}
		if running != nil {
			exited = running.done()
		}
		select {
		case <-ctx.Done():
			return nil
		case err := <-failed:
			return err
		case <-changes:
			apply()
		case <-exited:
			running = nil
			fmt.Fprintf(os.Stderr, "start-btmon: btmon exited; starting it again in %s\n", s.delay)
			retry = time.After(s.delay)
		case <-retry:
			retry = nil
			apply()
		}
	}
}

// readSetting answers whether the btmon file says true.
//
// A missing file is false, and so is any value other than true or
// false, and any failure to read the file. Off is the safe answer,
// because the trace prints keys in plain text.
func readSetting(path string) bool {
	contents, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "start-btmon: reading %s: %v; the trace stays off\n", path, err)
		return false
	}
	switch value := strings.TrimSpace(string(contents)); value {
	case "true":
		return true
	case "false":
		return false
	default:
		fmt.Fprintf(os.Stderr, "start-btmon: %s holds %q, not true or false; the trace stays off\n", path, value)
		return false
	}
}
