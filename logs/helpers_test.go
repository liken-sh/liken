package main

// These are fixtures shared across the relay tests. The kmsg and
// tailer tests both parse envelope streams, both need a fixed
// observation clock, and both test what happens when their output or
// checkpoint stops accepting writes.

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// observed is the fallback clock the tests use: the time that the
// relay's own clock read when it saw a line.
var observed = time.Date(2026, 7, 7, 15, 30, 0, 0, time.UTC)

// parseEnvelopes decodes a stream of envelope lines, and fails the
// test on anything that is not a valid envelope.
func parseEnvelopes(t *testing.T, raw string) []envelope {
	t.Helper()
	var out []envelope
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	for dec.More() {
		var e envelope
		if err := dec.Decode(&e); err != nil {
			t.Fatalf("output is not a stream of envelopes: %v\n%s", err, raw)
		}
		out = append(out, e)
	}
	return out
}

// errStdoutGone is what brokenWriter answers every write with.
var errStdoutGone = errors.New("stdout is gone")

// brokenWriter refuses every write. It represents a stdout whose
// reader has gone away. A relay that cannot send records must exit,
// and be restarted, rather than silently drop records.
type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) {
	return 0, errStdoutGone
}

// immediateCheckpoints makes every checkpoint write immediately, so a
// test can rely on the cursor showing the last record processed. A
// package variable is the seam used here, which is why tests in this
// package must not run in parallel.
func immediateCheckpoints(t *testing.T) {
	t.Helper()
	old := checkpointInterval
	checkpointInterval = 0
	t.Cleanup(func() { checkpointInterval = old })
}
