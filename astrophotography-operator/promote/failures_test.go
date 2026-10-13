package promote

// Each failure of the file system leaves the frame in the outbox and
// reports it, and the next run finishes the work.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/catalog"
)

// readOnly takes the write permission off a file or directory until the
// test ends.
func readOnly(t *testing.T, name string) {
	t.Helper()
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, info.Mode().Perm()&^0o222); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(name, info.Mode().Perm()) })
}

func TestAFailureLeavesTheFrameAndReportsIt(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through every permission")
	}
	cases := []struct {
		name    string
		prepare func(w *world, rel string)
		reason  string
	}{
		{"a frame that cannot be read", func(w *world, rel string) {
			if err := os.Chmod(filepath.Join(w.outbox, rel), 0); err != nil {
				t.Fatal(err)
			}
		}, ReasonCopyFailed},
		{"an archive that cannot be written", func(w *world, _ string) {
			if err := os.MkdirAll(w.archive, 0o755); err != nil {
				t.Fatal(err)
			}
			readOnly(t, w.archive)
		}, ReasonCopyFailed},
		{"a catalog that cannot be written", func(w *world, _ string) {
			name := filepath.Join(w.archive, "astro", "catalog", "m42.jsonl")
			w.write(name, nil)
			readOnly(t, name)
		}, ReasonCopyFailed},
		{"a file under the frame's name that cannot be read", func(w *world, _ string) {
			name := filepath.Join(w.archive, "astro", "M42_0001.fits")
			w.write(name, []byte("a file"))
			if err := os.Chmod(name, 0); err != nil {
				t.Fatal(err)
			}
		}, ReasonCopyFailed},
		{"a header with no END", func(w *world, rel string) {
			w.write(filepath.Join(w.outbox, rel), []byte(card("SIMPLE", "T")+string(make([]byte, 64*2880))))
		}, ReasonCopyFailed},
		{"a header with no NAXIS", func(w *world, rel string) {
			frame := lightFrame(frameID)
			copy(frame[2*80:], card("NOTAXIS", "2"))
			w.write(filepath.Join(w.outbox, rel), frame)
		}, ReasonCopyFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			rel := w.expose("M42_0001", lightFrame(frameID))
			c.prepare(w, rel)

			report, err := Run(w.options())

			if err != nil || len(report.Failures) != 1 || report.Failures[0].Reason != c.reason || report.Left != 1 {
				t.Errorf("the report is %+v, %v", report, err)
			}
			if _, err := os.Stat(filepath.Join(w.outbox, rel)); err != nil {
				t.Errorf("the frame left the outbox: %v", err)
			}
		})
	}
}

// An outbox that refuses the delete leaves the frame in the archive and
// in the outbox, recorded once. The next run deletes it.
func TestAnOutboxThatRefusesTheDeleteIsFinishedByTheNextRun(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through every permission")
	}
	w := newWorld(t)
	rel := w.expose("M42_0001", lightFrame(frameID))
	dir := filepath.Dir(filepath.Join(w.outbox, rel))
	readOnly(t, dir)

	first, err := Run(w.options())
	if err != nil || len(first.Failures) != 1 || first.Failures[0].Reason != ReasonCopyFailed {
		t.Fatalf("the first report is %+v, %v", first, err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := Run(w.options())

	_, counts := w.catalogLines()
	if err != nil || second.Promoted != 1 || counts[frameID] != 1 {
		t.Errorf("the second report is %+v, %v, with %d lines", second, err, counts[frameID])
	}
}

func TestRenameNoReplaceRefusesATakenName(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "from"), filepath.Join(dir, "to")
	w := &world{t: t}
	w.write(from, []byte("frame"))
	w.write(to, []byte("another frame"))
	cases := []struct {
		name   string
		rename func(from, to string) error
	}{
		{"renameat2", renameNoReplace},
		{"a link and an unlink", linkThenUnlink},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.rename(from, to); err == nil {
				t.Error("the rename replaced the file")
			}
		})
	}
}

// A share that refuses RENAME_NOREPLACE moves the file with a link and
// an unlink.
func TestLinkThenUnlinkMovesTheFile(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "from"), filepath.Join(dir, "to")
	w := &world{t: t}
	w.write(from, []byte("frame"))

	if err := linkThenUnlink(from, to); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Errorf("the old name is still there: %v", err)
	}
	if content, err := os.ReadFile(to); err != nil || string(content) != "frame" {
		t.Errorf("the new name holds %q, %v", content, err)
	}
}

// A frame whose catalog line records another hash is not the frame the
// line describes, so it stays in the outbox.
func TestAFrameThatDiffersFromItsLineFailsToVerify(t *testing.T) {
	cases := []struct {
		name      string
		sameClaim bool
	}{{"a copy", false}, {"a rename", true}}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			options := w.options()
			if c.sameClaim {
				w.archive = w.outbox
				options = w.options()
				options.SameClaim = true
			}
			line := catalog.Line{Record: catalog.RecordFrame, FrameID: frameID, Path: "M42_0001.fits", SHA256: "another hash"}
			if err := catalog.Append(filepath.Join(w.archive, "astro", "catalog", "m42.jsonl"), line); err != nil {
				t.Fatal(err)
			}
			rel := w.expose("M42_0001", lightFrame(frameID))

			report, err := Run(options)

			if err != nil || len(report.Failures) != 1 || report.Failures[0].Reason != ReasonVerifyFailed {
				t.Errorf("the report is %+v, %v", report, err)
			}
			if _, err := os.Stat(filepath.Join(w.outbox, rel)); err != nil {
				t.Errorf("the frame left the outbox: %v", err)
			}
			if _, err := os.Stat(filepath.Join(w.archive, "astro", "M42_0001.fits")); !os.IsNotExist(err) {
				t.Errorf("the archive holds the frame: %v", err)
			}
		})
	}
}

// On the same claim, a rename that the file system refuses leaves the
// frame in the outbox. Its line is recorded already, so the next run
// renames it without a second line.
func TestARefusedRenameOnTheSameClaimIsFinishedByTheNextRun(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through every permission")
	}
	w := newWorld(t)
	w.archive = w.outbox
	options := w.options()
	options.SameClaim = true
	rel := w.expose("M42_0001", lightFrame(frameID))
	archive := filepath.Join(w.outbox, "astro")
	if err := os.MkdirAll(filepath.Join(archive, catalog.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	readOnly(t, archive)

	first, err := Run(options)
	if err != nil || len(first.Failures) != 1 || first.Failures[0].Reason != ReasonCopyFailed {
		t.Fatalf("the first report is %+v, %v", first, err)
	}
	if err := os.Chmod(archive, 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := Run(options)

	_, counts := w.catalogLines()
	if err != nil || second.Promoted != 1 || counts[frameID] != 1 {
		t.Errorf("the second report is %+v, %v, with %d lines", second, err, counts[frameID])
	}
	if _, err := os.Stat(filepath.Join(w.outbox, rel)); !os.IsNotExist(err) {
		t.Errorf("the frame is still in the outbox: %v", err)
	}
}

// After a crash that came between the rename and the line, a catalog
// that cannot be written keeps the frame in the outbox, so the frame is
// never deleted without its line.
func TestAFrameWithNoLineIsNotDeleted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through every permission")
	}
	w := newWorld(t)
	rel := w.expose("M42_0001", lightFrame(frameID))
	if _, err := Run(crashAt(w.options(), stepPlaced)); !errors.Is(err, errCrash) {
		t.Fatal(err)
	}
	dir := filepath.Join(w.archive, "astro", catalog.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	readOnly(t, dir)

	report, err := Run(w.options())

	if err != nil || len(report.Failures) != 1 || report.Failures[0].Reason != ReasonCopyFailed {
		t.Errorf("the report is %+v, %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(w.outbox, rel)); err != nil {
		t.Errorf("the frame left the outbox: %v", err)
	}
}

// A directory of the target's frames that cannot be read stops the run,
// so the Job's next pod tries again, and a frame in it is never taken
// for gone.
func TestADirectoryThatCannotBeReadStopsTheRun(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads through every permission")
	}
	w := newWorld(t)
	rel := w.expose("M42/2026-10-12/M42_0001", lightFrame(frameID))
	dir := filepath.Dir(filepath.Join(w.outbox, rel))
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := Run(w.options()); err == nil {
		t.Error("the run answered no error")
	}
}
