package main

// The kmsg plumbing is tested at the seams that do not need the real
// device: the line splitter, which is pure; the drainer, fed through
// an io.Pipe into a fake kmsg; the console fallback, where the tests
// swap the package console variable for a buffer, the same pattern as
// disks_test.go; and the redirect itself, pointed at regular files for
// the two sysctls and at a FIFO for /dev/kmsg.

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/sys/unix"
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
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		syncLogs()
		if elapsed := time.Since(start); elapsed != 50*time.Millisecond {
			t.Errorf("syncLogs took %v, want its 50ms pause", elapsed)
		}
	})
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

// redirectFiles points the redirect's three kernel files into a
// temporary directory, as regular files, and restores the paths and
// init's output streams afterward. When the redirect replaced
// `os.Stdout` and `os.Stderr`, the cleanup closes the pipes it made,
// which ends its drainers.
func redirectFiles(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	savedPaths := []string{printkDevkmsgPath, printkPath, kmsgPath}
	savedOut, savedErr := os.Stdout, os.Stderr
	t.Cleanup(func() {
		printkDevkmsgPath, printkPath, kmsgPath = savedPaths[0], savedPaths[1], savedPaths[2]
		redirectedOut, redirectedErr := os.Stdout, os.Stderr
		os.Stdout, os.Stderr = savedOut, savedErr
		if redirectedOut != savedOut {
			redirectedOut.Close()
		}
		if redirectedErr != savedErr {
			redirectedErr.Close()
		}
	})
	printkDevkmsgPath = filepath.Join(dir, "printk_devkmsg")
	printkPath = filepath.Join(dir, "printk")
	kmsgPath = filepath.Join(dir, "kmsg")
	for _, path := range []string{printkDevkmsgPath, printkPath, kmsgPath} {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// kmsgFIFO replaces the redirect's /dev/kmsg with a FIFO and returns
// its read end, so the test reads each record as a drainer writes it.
// The read end opens first and without blocking, so the redirect's
// write-only open finds a reader.
func kmsgFIFO(t *testing.T) *os.File {
	t.Helper()
	if err := os.Remove(kmsgPath); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(kmsgPath, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := os.OpenFile(kmsgPath, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// readKmsgUntil reads records from the FIFO until every wanted record
// has arrived, and fails the test if they have not arrived in ten
// seconds.
func readKmsgUntil(t *testing.T, r *os.File, wants ...string) string {
	t.Helper()
	if err := r.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	chunk := make([]byte, 4096)
	for !kmsgHoldsAll(got.String(), wants) {
		n, err := r.Read(chunk)
		got.Write(chunk[:n])
		if err != nil {
			t.Fatalf("waiting for %q, read %q: %v", wants, got.String(), err)
		}
	}
	return got.String()
}

func kmsgHoldsAll(s string, wants []string) bool {
	return !slices.ContainsFunc(wants, func(want string) bool { return !strings.Contains(s, want) })
}

// The redirect turns off the kernel's rate limit on userspace records,
// sets the console to echo info records, and then carries init's
// stdout into /dev/kmsg as info records and its stderr as warning
// records. The liken-logs relay separates init's records from the
// kernel's by that priority.
func TestRedirectToKmsgCarriesInitsOutputIntoTheKernelLog(t *testing.T) {
	dir := redirectFiles(t)
	kmsg := kmsgFIFO(t)

	redirectToKmsg()
	fmt.Fprintln(os.Stderr, "liken: a warning")

	readKmsgUntil(t, kmsg,
		fmt.Sprintf("<%d>liken: init logs via /dev/kmsg from here on", kmsgInfo),
		fmt.Sprintf("<%d>liken: a warning", kmsgWarning))
	if got := readKernelFile(t, filepath.Join(dir, "printk_devkmsg")); got != "on\n" {
		t.Errorf("the rate limit is turned off: printk_devkmsg = %q", got)
	}
	if got := readKernelFile(t, filepath.Join(dir, "printk")); got != "7" {
		t.Errorf("the console echoes info records: printk = %q", got)
	}
}

func readKernelFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// A redirect that cannot turn off the rate limit, or cannot open
// /dev/kmsg, leaves init's output on the console. A rate-limited
// redirect would drop lines that the console shows.
func TestRedirectToKmsgStaysOnTheConsoleWhenTheKernelRefuses(t *testing.T) {
	cases := []struct {
		name string
		path *string
	}{
		{"printk_devkmsg is not writable", &printkDevkmsgPath},
		{"/dev/kmsg does not open", &kmsgPath},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			dir := redirectFiles(t)
			*one.path = filepath.Join(dir, "missing", "file")
			stdout, stderr := os.Stdout, os.Stderr

			redirectToKmsg()

			if os.Stdout != stdout || os.Stderr != stderr {
				t.Error("init's output stays on the console")
			}
		})
	}
}

// The console log level is best effort. A refused write to printk
// leaves the echo as the kernel set it, and the redirect still carries
// init's output into /dev/kmsg, because the ring buffer is the record.
func TestRedirectToKmsgSurvivesARefusedLogLevel(t *testing.T) {
	dir := redirectFiles(t)
	printkPath = filepath.Join(dir, "missing", "printk")
	kmsg := kmsgFIFO(t)

	redirectToKmsg()

	readKmsgUntil(t, kmsg, fmt.Sprintf("<%d>liken: init logs via /dev/kmsg from here on", kmsgInfo))
}
