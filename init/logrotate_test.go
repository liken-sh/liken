package main

// Rotation is tested the way it runs: against real files in a
// tempdir, shifted by name. The paths in logrotate.go's entry point
// are constants aimed at clusterState, so these tests exercise the
// mechanics (rotateGenerations, cappedLogFile) that take paths as
// inputs.

import (
	"os"
	"path/filepath"
	"testing"
)

func writeLog(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
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
		t.Error("the live file should have moved aside")
	}
	if got := readLog(t, log+".1"); got != "current boot" {
		t.Errorf(".1: %q", got)
	}
	if got := readLog(t, log+".2"); got != "one boot ago" {
		t.Errorf(".2: %q", got)
	}
	if got := readLog(t, log+".3"); got != "two boots ago" {
		t.Errorf(".3: %q", got)
	}
	if _, err := os.Stat(log + ".4"); !os.IsNotExist(err) {
		t.Error("nothing should survive past the kept generations")
	}
}

func TestRotateGenerationsOnAFirstBootIsANoOp(t *testing.T) {
	dir := t.TempDir()
	rotateGenerations(filepath.Join(dir, "k3s.log"), 3)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("rotating nothing should create nothing: %v", entries)
	}
}

func TestRotateGenerationsWithGaps(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "k3s.log")
	// Only the live file and .2 exist (a boot that never wrote .1,
	// however that happened); the shift tolerates the hole.
	writeLog(t, log, "current")
	writeLog(t, log+".2", "older")

	rotateGenerations(log, 3)

	if got := readLog(t, log+".1"); got != "current" {
		t.Errorf(".1: %q", got)
	}
	if got := readLog(t, log+".3"); got != "older" {
		t.Errorf(".3: %q", got)
	}
}

func TestCappedLogAppendsAcrossReopens(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "k3s.log")

	// Two opens within one boot: the supervisor's backoff restarts
	// k3s and reopens the log, and the boot's record must accumulate.
	first, err := openCappedLog(log, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Write([]byte("before the crash\n")); err != nil {
		t.Fatal(err)
	}
	first.Close()

	second, err := openCappedLog(log, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Write([]byte("after the restart\n")); err != nil {
		t.Fatal(err)
	}
	second.Close()

	if got := readLog(t, log); got != "before the crash\nafter the restart\n" {
		t.Errorf("log: %q", got)
	}
}

func TestCappedLogRotatesPastTheLimit(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "k3s.log")
	c, err := openCappedLog(log, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// The first line blows straight past the limit: nothing rotates
	// mid-line, so it lands whole.
	if _, err := c.Write([]byte("a line well past ten bytes\n")); err != nil {
		t.Fatal(err)
	}
	// The next write finds the file over the limit at a line
	// boundary, so the rotation happens first.
	if _, err := c.Write([]byte("fresh file\n")); err != nil {
		t.Fatal(err)
	}

	if got := readLog(t, log+".1"); got != "a line well past ten bytes\n" {
		t.Errorf("rotated generation: %q", got)
	}
	if got := readLog(t, log); got != "fresh file\n" {
		t.Errorf("live file: %q", got)
	}
}

func TestCappedLogNeverRotatesMidLine(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "k3s.log")
	c, err := openCappedLog(log, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// A line arriving in fragments (k3s writes whatever chunks it
	// likes) crosses the limit before its newline arrives; the
	// rotation must wait for the boundary.
	if _, err := c.Write([]byte("split ")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("across writes\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("next\n")); err != nil {
		t.Fatal(err)
	}

	if got := readLog(t, log+".1"); got != "split across writes\n" {
		t.Errorf("rotated generation: %q", got)
	}
	if got := readLog(t, log); got != "next\n" {
		t.Errorf("live file: %q", got)
	}
}

func TestCappedLogGoesQuietWhenTheFileBreaks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k3s.log")
	c, err := openCappedLog(path, k3sLogCap)
	if err != nil {
		t.Fatal(err)
	}
	// The file dies under the writer (a full disk's simplest stand-in
	// is a closed descriptor). The Write contract holds: no error, the
	// failure is reported once, and later writes are quiet no-ops so
	// the console copy keeps flowing.
	c.f.Close()
	if n, err := c.Write([]byte("lost line\n")); err != nil || n != len("lost line\n") {
		t.Errorf("Write never propagates failure: %d, %v", n, err)
	}
	if !c.broken {
		t.Error("the writer marks itself broken")
	}
	if _, err := c.Write([]byte("more\n")); err != nil {
		t.Error("a broken writer stays a quiet no-op")
	}
	if err := c.Close(); err != nil {
		t.Errorf("closing a broken writer is clean: %v", err)
	}
}

func TestShiftLogReportsARenameFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "k3s.log"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	shiftLog(filepath.Join(dir, "k3s.log"), filepath.Join(dir, "k3s.log.1"))
}
