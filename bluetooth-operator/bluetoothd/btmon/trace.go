package main

// The supervisor runs btmon, the Bluetooth packet trace, while the
// btmon setting file contains true and stops it otherwise.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

// restartDelay is how long the supervisor waits before it starts btmon
// again after an exit or a failed start. Without it, a btmon that
// fails at once, such as one without the NET_RAW capability, would
// restart in a tight loop and fill the log.
const restartDelay = 5 * time.Second

// trace is a running btmon. The supervisor starts it through a
// startFunc, so a test can substitute a fake.
type trace interface {
	// done closes when the process has exited.
	done() <-chan struct{}

	// stop ends the process and returns when it has exited.
	stop()
}

type startFunc func() (trace, error)

// supervisor runs one trace while the file at setting contains true.
type supervisor struct {
	setting string
	start   startFunc
	delay   time.Duration
}

// run reads the setting, then reads it again on each value received
// from changes, and starts or stops the trace to match. It returns nil
// when ctx ends, or the error received from failed. It stops the trace
// before it returns.
func (s *supervisor) run(ctx context.Context, changes <-chan struct{}, failed <-chan error) error {
	var running trace
	// retry is nil when no restart is pending.
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

// readSetting reports whether the file at path contains true. A
// missing file, an unreadable file, and any other value count as
// false, because the trace prints keys in plain text and must be
// enabled deliberately.
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
