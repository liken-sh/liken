package main

// The kmsg plumbing is tested at the seams that do not need the real
// device: the line splitter, which is pure; the drainer, fed through
// an io.Pipe into a fake kmsg; and the console fallback, where the
// tests swap the package console variable for a buffer, the same
// pattern as disks_test.go.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
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

// collectingWriter gathers each Write as one record, the same way
// /dev/kmsg treats each write. It is safe for the drainer goroutine
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
	return slices.Clone(w.records)
}

// drained runs the drainer over a pipe. It returns a write function
// for the test to feed the drainer, and a close function that waits
// for the drainer to finish.
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
	// The code must not have shipped the fragment yet.
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
// reboot path calls it, so the pause must never grow larger.
func TestSyncLogsIsBounded(t *testing.T) {
	start := time.Now()
	syncLogs()
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("syncLogs took %v", elapsed)
	}
}

// A pin on the priority arithmetic: facility 1, severities 6 and 4,
// exactly what the liken-logs relay filters for.
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

func TestDrainToKmsgCarriesWholeLines(t *testing.T) {
	var kmsg bytes.Buffer
	// Two complete lines and a trailing fragment. The fragment must
	// wait for its newline, which EOF never delivers, so exactly two
	// records come out.
	drainToKmsg(strings.NewReader("first\nsecond\nfragment"), &kmsg, kmsgInfo)
	want := "<14>first<14>second"
	if kmsg.String() != want {
		t.Errorf("got %q, want %q", kmsg.String(), want)
	}
}

// panickingWriter stands in for a kmsg device so broken that writing
// to it panics, the worst case that emitKmsgLine promises to
// absorb.
type panickingWriter struct{}

func (panickingWriter) Write([]byte) (int, error) { panic("the device is gone") }

func TestEmitKmsgLineSurvivesAPanickingWriter(t *testing.T) {
	old := console
	var fallback bytes.Buffer
	console = &fallback
	t.Cleanup(func() { console = old })
	emitKmsgLine(panickingWriter{}, kmsgWarning, []byte("must not be lost"))
	if !strings.Contains(fallback.String(), "must not be lost") {
		t.Errorf("the line falls back to the raw console: %q", fallback.String())
	}
}
