package main

// These tests rotate real files in a temporary directory. rotateBootLogs
// uses fixed paths, so the tests call rotateGenerations and
// openCappedLog, which take the path as an argument.

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func writeLog(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("writing the log file: %v", err)
	}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the log file: %v", err)
	}
	return string(data)
}

func TestRotateGenerationsShiftsEveryFileDown(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "k3s.log")
	writeLog(t, log, "current boot")
	writeLog(t, log+".1", "one boot ago")
	writeLog(t, log+".2", "two boots ago")
	writeLog(t, log+".3", "three boots ago, about to fall off")

	rotateGenerations(log, 3)

	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Error("the live file exists, want it renamed to .1")
	}
	if got := readLog(t, log+".1"); got != "current boot" {
		t.Errorf("generation .1 holds %q, want the live file's contents", got)
	}
	if got := readLog(t, log+".2"); got != "one boot ago" {
		t.Errorf("generation .2 holds %q, want one boot ago", got)
	}
	if got := readLog(t, log+".3"); got != "two boots ago" {
		t.Errorf("generation .3 holds %q, want two boots ago", got)
	}
	if _, err := os.Stat(log + ".4"); !os.IsNotExist(err) {
		t.Error("generation .4 exists, want it deleted")
	}
}

func TestRotateGenerationsCreatesNothingWhenNoLogExists(t *testing.T) {
	dir := t.TempDir()
	rotateGenerations(filepath.Join(dir, "k3s.log"), 3)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing the log directory: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("the directory holds %v after rotating no log, want it empty", entries)
	}
}

func TestRotateGenerationsWithGaps(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "k3s.log")
	// .1 is missing, and the rotation must skip it.
	writeLog(t, log, "current")
	writeLog(t, log+".2", "older")

	rotateGenerations(log, 3)

	if got := readLog(t, log+".1"); got != "current" {
		t.Errorf("generation .1 holds %q, want the live file's contents", got)
	}
	if got := readLog(t, log+".3"); got != "older" {
		t.Errorf("generation .3 holds %q, want the contents of .2", got)
	}
}

func TestCappedLogAppendsAcrossReopens(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "k3s.log")

	// The supervisor reopens the log each time it restarts k3s.
	first, err := openCappedLog(log, 1<<20)
	if err != nil {
		t.Fatalf("opening the capped log: %v", err)
	}
	if _, err := first.Write([]byte("before the crash\n")); err != nil {
		t.Fatalf("writing to the capped log: %v", err)
	}
	first.Close()

	second, err := openCappedLog(log, 1<<20)
	if err != nil {
		t.Fatalf("opening the capped log: %v", err)
	}
	if _, err := second.Write([]byte("after the restart\n")); err != nil {
		t.Fatalf("writing to the capped log: %v", err)
	}
	second.Close()

	if got := readLog(t, log); got != "before the crash\nafter the restart\n" {
		t.Errorf("the log holds %q, want the lines of both runs", got)
	}
}

func TestCappedLogRotatesPastTheLimit(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "k3s.log")
	c, err := openCappedLog(log, 10)
	if err != nil {
		t.Fatalf("opening the capped log: %v", err)
	}
	defer c.Close()

	// This line is longer than the limit. The writer does not rotate
	// in the middle of a line, so the whole line goes to the file.
	if _, err := c.Write([]byte("a line well past ten bytes\n")); err != nil {
		t.Fatalf("writing to the capped log: %v", err)
	}
	// The file is over the limit and ends at a line boundary, so this
	// write rotates first.
	if _, err := c.Write([]byte("fresh file\n")); err != nil {
		t.Fatalf("writing to the capped log: %v", err)
	}

	if got := readLog(t, log+".1"); got != "a line well past ten bytes\n" {
		t.Errorf("generation .1 holds %q, want the full log", got)
	}
	if got := readLog(t, log); got != "fresh file\n" {
		t.Errorf("the live file holds %q, want only the new line", got)
	}
}

func TestCappedLogNeverRotatesMidLine(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "k3s.log")
	c, err := openCappedLog(log, 4)
	if err != nil {
		t.Fatalf("opening the capped log: %v", err)
	}
	defer c.Close()

	// k3s can write one line in several chunks. This line passes the
	// limit before its newline arrives, and the rotation must wait for
	// the newline.
	if _, err := c.Write([]byte("split ")); err != nil {
		t.Fatalf("writing to the capped log: %v", err)
	}
	if _, err := c.Write([]byte("across writes\n")); err != nil {
		t.Fatalf("writing to the capped log: %v", err)
	}
	if _, err := c.Write([]byte("next\n")); err != nil {
		t.Fatalf("writing to the capped log: %v", err)
	}

	if got := readLog(t, log+".1"); got != "split across writes\n" {
		t.Errorf("generation .1 holds %q, want the full log", got)
	}
	if got := readLog(t, log); got != "next\n" {
		t.Errorf("the live file holds %q, want only the new line", got)
	}
}

func TestCappedLogStopsWritingTheFileAfterAWriteFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k3s.log")
	c, err := openCappedLog(path, k3sLogCap)
	if err != nil {
		t.Fatalf("opening the capped log: %v", err)
	}
	// Closing the descriptor makes the next write fail, as a full disk
	// would.
	c.f.Close()
	if n, err := c.Write([]byte("lost line\n")); err != nil || n != len("lost line\n") {
		t.Errorf("Write returned %d and %v, want the length of the line and no error", n, err)
	}
	if !c.broken {
		t.Error("broken is false, want true")
	}
	if _, err := c.Write([]byte("more\n")); err != nil {
		t.Error("Write on a broken writer returned an error, want none")
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close on a broken writer returned %v, want nil", err)
	}
}

func TestShiftLogContinuesAfterARenameFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "k3s.log"), []byte("x"), 0o644); err != nil {
		t.Fatalf("writing the log file: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	shiftLog(filepath.Join(dir, "k3s.log"), filepath.Join(dir, "k3s.log.1"))
}

func TestOpenCappedLogReportsAMissingDirectory(t *testing.T) {
	if _, err := openCappedLog(filepath.Join(t.TempDir(), "missing", "k3s.log"), k3sLogCap); err == nil {
		t.Error("openCappedLog returned no error, want one for a missing directory")
	}
}

// When the oldest generation cannot be removed, the rotation still
// renames the live file to .1, so the boot starts a new log.
func TestRotateGenerationsContinuesPastAStuckOldestGeneration(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "k3s.log")
	writeLog(t, log, "current boot")
	if err := os.MkdirAll(filepath.Join(log+".3", "stuck"), 0o755); err != nil {
		t.Fatalf("creating the blocking directory: %v", err)
	}

	rotateGenerations(log, 3)

	if got := readLog(t, log+".1"); got != "current boot" {
		t.Errorf("generation .1 holds %q, want the live file's contents", got)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Error("the live file exists, want it renamed to .1")
	}
}

// When closing the file fails at the cap, the writer still rotates and
// continues on a new file. A failed close can lose the last bytes of
// the old file, but later output is not lost.
func TestCappedLogRotatesPastAFailedClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k3s.log")
	c, err := openCappedLog(path, 4)
	if err != nil {
		t.Fatalf("opening the capped log: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	if _, err := c.Write([]byte("full\n")); err != nil {
		t.Fatalf("writing to the capped log: %v", err)
	}
	c.f.Close()

	if _, err := c.Write([]byte("next\n")); err != nil {
		t.Fatalf("writing to the capped log: %v", err)
	}

	if got := readLog(t, path+".1"); got != "full\n" {
		t.Errorf("generation .1 holds %q, want the full log", got)
	}
	if got := readLog(t, path); got != "next\n" {
		t.Errorf("the live file holds %q, want only the new line", got)
	}
}

func TestCappedLogStopsWritingWhenItCannotReopenAfterRotation(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "liken")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("creating the log directory: %v", err)
	}
	c, err := openCappedLog(filepath.Join(dir, "k3s.log"), 4)
	if err != nil {
		t.Fatalf("opening the capped log: %v", err)
	}
	if _, err := c.Write([]byte("full\n")); err != nil {
		t.Fatalf("writing to the capped log: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("removing the log directory: %v", err)
	}

	n, err := c.Write([]byte("next\n"))

	if n != len("next\n") || err != nil {
		t.Errorf("Write returned %d and %v, want the length of the line and no error", n, err)
	}
	if !c.broken {
		t.Error("broken is false, want true")
	}
}

// k3s writes to stdout and stderr at the same time, and both go to one
// capped log whose size counter and rotation have no lock. The race
// detector fails this test if two goroutines write the log.
func TestTeeOutputWritesTheLogFromOneGoroutine(t *testing.T) {
	logf, err := openCappedLog(filepath.Join(t.TempDir(), "k3s.log"), 256)
	if err != nil {
		t.Fatalf("opening the capped log: %v", err)
	}
	cmd := exec.Command("sh", "-c", `for i in $(seq 1000); do echo "out $i"; echo "err $i" >&2; done`)
	teeOutput(cmd, logf, io.Discard)

	if err := cmd.Run(); err != nil {
		t.Fatalf("running the command: %v", err)
	}
}
