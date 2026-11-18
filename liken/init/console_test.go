package main

// These tests do not open the real /dev/kmsg. The tests of
// redirectToKmsg point the two sysctl paths at regular files and
// kmsgPath at a FIFO.

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
		t.Errorf("a short line was split into %q, want it unchanged", parts)
	}
}

func TestSplitKmsgLineMarksTheCuts(t *testing.T) {
	parts := splitKmsgLine([]byte("abcdefghij"), 4)
	want := []string{"abcd ...", "... efgh ...", "... ij"}
	if len(parts) != len(want) {
		t.Fatalf("the line was split into %d parts %q, want %d", len(parts), parts, len(want))
	}
	for i, part := range parts {
		if string(part) != want[i] {
			t.Errorf("part %d is %q, want %q", i, part, want[i])
		}
	}
}

func TestSplitKmsgLineAtAnExactBoundary(t *testing.T) {
	parts := splitKmsgLine([]byte("abcdefgh"), 4)
	want := []string{"abcd ...", "... efgh"}
	if len(parts) != len(want) {
		t.Fatalf("the line was split into %d parts %q, want %d", len(parts), parts, len(want))
	}
	for i, part := range parts {
		if string(part) != want[i] {
			t.Errorf("part %d is %q, want %q", i, part, want[i])
		}
	}
}

// collectingWriter keeps each Write as one record, as /dev/kmsg does.
// The drain goroutine and the test can use it at the same time.
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

// drained runs drainToKmsg on a pipe. It returns a function that
// writes to the pipe and a function that closes the pipe and waits for
// drainToKmsg to return.
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
			t.Fatalf("writing to the drained pipe: %v", err)
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
		t.Fatalf("the drainer wrote %d records %q, want 2", len(records), records)
	}
	if records[0] != "<14>liken: first" || records[1] != "<14>liken: second" {
		t.Errorf("the records are %q, want <14>liken: first and <14>liken: second", records)
	}
}

func TestDrainerWaitsForTheNewlineOfAFragment(t *testing.T) {
	kmsg := &collectingWriter{}
	write, stop := drained(t, kmsg, kmsgWarning)
	write("liken: a line arriving ")
	if got := kmsg.snapshot(); len(got) != 0 {
		t.Fatalf("the drainer wrote %q before the line ended, want no records", got)
	}
	write("in two writes\n")
	stop()
	records := kmsg.snapshot()
	if len(records) != 1 || records[0] != "<12>liken: a line arriving in two writes" {
		t.Errorf("the records are %q, want one record with the whole line", records)
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
		t.Errorf("the console received %q, want the line that kmsg refused", fallback.String())
	}
}

func TestEmitKmsgLineSplitsLongLines(t *testing.T) {
	kmsg := &collectingWriter{}
	long := strings.Repeat("x", kmsgPayloadLimit+10)
	emitKmsgLine(kmsg, kmsgInfo, []byte(long))
	records := kmsg.snapshot()
	if len(records) != 2 {
		t.Fatalf("a line of %d bytes became %d records, want 2", len(long), len(records))
	}
	for _, rec := range records {
		if len(rec) > kmsgPayloadLimit+len("<14>")+len("... ")+len(" ...") {
			t.Errorf("a record is %d bytes, want at most the kernel limit with its marks", len(rec))
		}
	}
}

// The shutdown paths call syncLogs just before a reboot, so it must
// wait 50ms and no longer.
func TestSyncLogsIsBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		syncLogs()
		if elapsed := time.Since(start); elapsed != 50*time.Millisecond {
			t.Errorf("syncLogs took %v, want its 50ms pause", elapsed)
		}
	})
}

// The priorities are facility 1 with severities 6 and 4, which is what
// the liken-logs relay filters for.
func TestKmsgPriorities(t *testing.T) {
	if kmsgInfo != 14 {
		t.Errorf("kmsgInfo is %d, want 14", kmsgInfo)
	}
	if kmsgWarning != 12 {
		t.Errorf("kmsgWarning is %d, want 12", kmsgWarning)
	}
	if fmt.Sprintf("<%d>", kmsgInfo) != "<14>" {
		t.Errorf("the priority prefix is %q, want <14>", fmt.Sprintf("<%d>", kmsgInfo))
	}
}

func TestDrainToKmsgWritesOnlyCompleteLines(t *testing.T) {
	var kmsg bytes.Buffer
	// The reader reaches EOF before the last line has its newline.
	drainToKmsg(strings.NewReader("first\nsecond\nfragment"), &kmsg, kmsgInfo)
	want := "<14>first<14>second"
	if kmsg.String() != want {
		t.Errorf("the records are %q, want %q", kmsg.String(), want)
	}
}

type panickingWriter struct{}

func (panickingWriter) Write([]byte) (int, error) { panic("the device is gone") }

func TestEmitKmsgLineSurvivesAPanickingWriter(t *testing.T) {
	old := console
	var fallback bytes.Buffer
	console = &fallback
	t.Cleanup(func() { console = old })
	emitKmsgLine(panickingWriter{}, kmsgWarning, []byte("must not be lost"))
	if !strings.Contains(fallback.String(), "must not be lost") {
		t.Errorf("the console received %q, want the line that the writer panicked on", fallback.String())
	}
}

// redirectFiles creates the files that redirectToKmsg writes as
// regular files in a temporary directory and points the path variables
// at them. The cleanup restores the paths, os.Stdout, and os.Stderr,
// and closes the pipes that the redirect created, which stops the
// drain goroutines.
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
			t.Fatalf("creating the file %s: %v", path, err)
		}
	}
	return dir
}

// kmsgFIFO replaces the file at kmsgPath with a FIFO and returns its
// read end, so a test can read each record as it is written. The read
// end opens first and without blocking, because opening a FIFO
// write-only fails until a reader exists.
func kmsgFIFO(t *testing.T) *os.File {
	t.Helper()
	if err := os.Remove(kmsgPath); err != nil {
		t.Fatalf("removing the kmsg file: %v", err)
	}
	if err := unix.Mkfifo(kmsgPath, 0o600); err != nil {
		t.Fatalf("creating the kmsg FIFO: %v", err)
	}
	r, err := os.OpenFile(kmsgPath, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		t.Fatalf("opening the kmsg FIFO: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

// readKmsgUntil reads from the FIFO until all of wants have appeared
// and returns what it read. It fails the test after ten seconds.
func readKmsgUntil(t *testing.T, r *os.File, wants ...string) string {
	t.Helper()
	if err := r.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatalf("setting the read deadline on the kmsg FIFO: %v", err)
	}
	var got strings.Builder
	chunk := make([]byte, 4096)
	for !kmsgContainsAll(got.String(), wants) {
		n, err := r.Read(chunk)
		got.Write(chunk[:n])
		if err != nil {
			t.Fatalf("waiting for %q, read %q: %v", wants, got.String(), err)
		}
	}
	return got.String()
}

func kmsgContainsAll(s string, wants []string) bool {
	return !slices.ContainsFunc(wants, func(want string) bool { return !strings.Contains(s, want) })
}

// The redirect disables the kernel's rate limit on userspace records,
// sets the console to echo info records, and writes init's stdout to
// /dev/kmsg as info records and its stderr as warning records.
func TestRedirectToKmsgWritesInitOutputToKmsg(t *testing.T) {
	dir := redirectFiles(t)
	kmsg := kmsgFIFO(t)

	redirectToKmsg()
	fmt.Fprintln(os.Stderr, "liken: a warning")

	readKmsgUntil(t, kmsg,
		fmt.Sprintf("<%d>liken: init now writes its output to /dev/kmsg", kmsgInfo),
		fmt.Sprintf("<%d>liken: a warning", kmsgWarning))
	if got := readKernelFile(t, filepath.Join(dir, "printk_devkmsg")); got != "on\n" {
		t.Errorf("printk_devkmsg is %q, want \"on\\n\" to turn off the rate limit", got)
	}
	if got := readKernelFile(t, filepath.Join(dir, "printk")); got != "7" {
		t.Errorf("printk is %q, want \"7\" so the console echoes info records", got)
	}
}

func readKernelFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

// When the redirect cannot disable the rate limit or cannot open
// /dev/kmsg, it leaves init's output on the console, because a
// rate-limited redirect would drop lines that the console shows.
func TestRedirectToKmsgLeavesOutputOnTheConsoleWhenAFileFails(t *testing.T) {
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
				t.Error("os.Stdout or os.Stderr changed, want init's output to stay on the console")
			}
		})
	}
}

// A failed write to printk leaves the console log level as the kernel
// set it. The redirect still writes init's output to /dev/kmsg.
func TestRedirectToKmsgContinuesWhenTheLogLevelWriteFails(t *testing.T) {
	dir := redirectFiles(t)
	printkPath = filepath.Join(dir, "missing", "printk")
	kmsg := kmsgFIFO(t)

	redirectToKmsg()

	readKmsgUntil(t, kmsg, fmt.Sprintf("<%d>liken: init now writes its output to /dev/kmsg", kmsgInfo))
}
