package main

// The kmsg plumbing is tested at the seams that don't need the real
// device: the line splitter (pure), the drainer (fed through an
// io.Pipe into a fake kmsg), and the console fallback (the package
// console variable swapped for a buffer, the disks_test.go pattern).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSplitKmsgLineLeavesShortLinesAlone(t *testing.T) {
	parts := splitKmsgLine([]byte("liken: hello from userspace"), 800)
	if len(parts) != 1 || string(parts[0]) != "liken: hello from userspace" {
		t.Errorf("got %q", parts)
	}
}

func TestSplitKmsgLineMarksTheCuts(t *testing.T) {
	parts := splitKmsgLine([]byte("abcdefghij"), 4)
	want := []string{"abcd ...", "... efgh ...", "... ij"}
	if len(parts) != len(want) {
		t.Fatalf("got %d parts, want %d: %q", len(parts), len(want), parts)
	}
	for i, part := range parts {
		if string(part) != want[i] {
			t.Errorf("part %d: got %q, want %q", i, part, want[i])
		}
	}
}

func TestSplitKmsgLineAtAnExactBoundary(t *testing.T) {
	parts := splitKmsgLine([]byte("abcdefgh"), 4)
	want := []string{"abcd ...", "... efgh"}
	if len(parts) != len(want) {
		t.Fatalf("got %d parts, want %d: %q", len(parts), len(want), parts)
	}
	for i, part := range parts {
		if string(part) != want[i] {
			t.Errorf("part %d: got %q, want %q", i, part, want[i])
		}
	}
}

// collectingWriter gathers each Write as one record, the way
// /dev/kmsg treats each write, and is safe for the drainer goroutine
// and the test to share.
type collectingWriter struct {
	mu      sync.Mutex
	records []string
	fail    error
}

func (w *collectingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fail != nil {
		return 0, w.fail
	}
	w.records = append(w.records, string(p))
	return len(p), nil
}

func (w *collectingWriter) snapshot() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.records...)
}

// drained runs the drainer over a pipe, returning a write function
// for the test to feed it and a close function that waits for the
// drainer to finish.
func drained(t *testing.T, kmsg io.Writer, priority int) (func(string), func()) {
	t.Helper()
	r, w := io.Pipe()
	done := make(chan struct{})
	go func() {
		drainToKmsg(r, kmsg, priority)
		close(done)
	}()
	write := func(s string) {
		if _, err := io.WriteString(w, s); err != nil {
			t.Fatal(err)
		}
	}
	stop := func() {
		w.Close()
		<-done
	}
	return write, stop
}

func TestDrainerWritesOneRecordPerLine(t *testing.T) {
	kmsg := &collectingWriter{}
	write, stop := drained(t, kmsg, kmsgInfo)
	write("liken: first\nliken: second\n")
	stop()
	records := kmsg.snapshot()
	if len(records) != 2 {
		t.Fatalf("got %d records: %q", len(records), records)
	}
	if records[0] != "<14>liken: first" || records[1] != "<14>liken: second" {
		t.Errorf("records: %q", records)
	}
}

func TestDrainerHoldsFragmentsForTheirNewline(t *testing.T) {
	kmsg := &collectingWriter{}
	write, stop := drained(t, kmsg, kmsgWarning)
	write("liken: a line arriving ")
	// The fragment must not have been shipped yet.
	if got := kmsg.snapshot(); len(got) != 0 {
		t.Fatalf("a fragment shipped early: %q", got)
	}
	write("in two writes\n")
	stop()
	records := kmsg.snapshot()
	if len(records) != 1 || records[0] != "<12>liken: a line arriving in two writes" {
		t.Errorf("records: %q", records)
	}
}

func TestDrainerFallsBackToTheConsole(t *testing.T) {
	var fallback bytes.Buffer
	oldConsole := console
	console = &fallback
	t.Cleanup(func() { console = oldConsole })

	kmsg := &collectingWriter{fail: errors.New("kmsg refused")}
	write, stop := drained(t, kmsg, kmsgInfo)
	write("liken: must not vanish\n")
	stop()
	if fallback.String() != "liken: must not vanish\n" {
		t.Errorf("the line should land on the console: %q", fallback.String())
	}
}

func TestEmitKmsgLineSplitsLongLines(t *testing.T) {
	kmsg := &collectingWriter{}
	long := strings.Repeat("x", kmsgPayloadLimit+10)
	emitKmsgLine(kmsg, kmsgInfo, []byte(long))
	records := kmsg.snapshot()
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2: lengths %d", len(records), len(long))
	}
	for _, rec := range records {
		if len(rec) > kmsgPayloadLimit+len("<14>")+len("... ")+len(" ...") {
			t.Errorf("record too long for the kernel: %d bytes", len(rec))
		}
	}
}

// syncLogs is a bounded pause, and the bound is the contract: a
// reboot path calls it, so it must never balloon.
func TestSyncLogsIsBounded(t *testing.T) {
	start := time.Now()
	syncLogs()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("syncLogs took %v", elapsed)
	}
}

// A sanity pin on the priority arithmetic: facility 1, severities 6
// and 4, exactly what the liken-logs relay filters for.
func TestKmsgPriorities(t *testing.T) {
	if kmsgInfo != 14 {
		t.Errorf("info priority: %d", kmsgInfo)
	}
	if kmsgWarning != 12 {
		t.Errorf("warning priority: %d", kmsgWarning)
	}
	if fmt.Sprintf("<%d>", kmsgInfo) != "<14>" {
		t.Error("the prefix format changed")
	}
}
