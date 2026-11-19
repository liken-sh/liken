package promote

// A file system failure leaves the frame in the outbox and appears in
// the report, and the next run finishes the work.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/catalog"
)

// readOnly removes the write permission from a file or directory until
// the test ends.
func readOnly(t *testing.T, name string) {
	t.Helper()
	info, err := os.Stat(name)
	if err != nil {
		t.Fatalf("reading the mode of %s: %v", name, err)
	}
	if err := os.Chmod(name, info.Mode().Perm()&^0o222); err != nil {
		t.Fatalf("removing the write permission of %s: %v", name, err)
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
				t.Fatalf("removing the permissions of the frame: %v", err)
			}
		}, ReasonCopyFailed},
		{"an archive that cannot be written", func(w *world, _ string) {
			if err := os.MkdirAll(w.archive, 0o755); err != nil {
				t.Fatalf("creating the archive directory: %v", err)
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
				t.Fatalf("removing the permissions of %s: %v", name, err)
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
				t.Errorf("Run returned the report %+v and the error %v, want no error, 1 failure with the reason %s, and 1 frame left", report, err, c.reason)
			}
			if _, err := os.Stat(filepath.Join(w.outbox, rel)); err != nil {
				t.Errorf("the frame is not in the outbox, want it left there: %v", err)
			}
		})
	}
}

// When the outbox refuses the delete, the frame is in both the archive
// and the outbox, with one catalog line. The next run deletes it.
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
		t.Fatalf("the first run returned the report %+v and the error %v, want no error and 1 failure with the reason %s", first, err, ReasonCopyFailed)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("restoring the permissions of %s: %v", dir, err)
	}
	second, err := Run(w.options())

	_, counts := w.catalogLines()
	if err != nil || second.Promoted != 1 || counts[frameID] != 1 {
		t.Errorf("the second run returned the report %+v and the error %v, with %d catalog lines for the frame, want no error, 1 promoted frame, and 1 line", second, err, counts[frameID])
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
				t.Error("the rename replaced the existing file, want it refused")
			}
		})
	}
}

// For a file system that refuses RENAME_NOREPLACE, linkThenUnlink moves
// the file with a link and then an unlink.
func TestLinkThenUnlinkMovesTheFile(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "from"), filepath.Join(dir, "to")
	w := &world{t: t}
	w.write(from, []byte("frame"))

	if err := linkThenUnlink(from, to); err != nil {
		t.Fatalf("moving the file: %v", err)
	}

	if _, err := os.Stat(from); !os.IsNotExist(err) {
		t.Errorf("Stat of the old name returned %v, want a not-exist error", err)
	}
	if content, err := os.ReadFile(to); err != nil || string(content) != "frame" {
		t.Errorf("the new name holds %q and read with the error %v, want \"frame\" and no error", content, err)
	}
}

// A frame whose hash differs from the hash in its catalog line is not
// the frame that the line describes, so it stays in the outbox.
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
				t.Fatalf("appending the catalog line: %v", err)
			}
			rel := w.expose("M42_0001", lightFrame(frameID))

			report, err := Run(options)

			if err != nil || len(report.Failures) != 1 || report.Failures[0].Reason != ReasonVerifyFailed {
				t.Errorf("Run returned the report %+v and the error %v, want no error and 1 failure with the reason %s", report, err, ReasonVerifyFailed)
			}
			if _, err := os.Stat(filepath.Join(w.outbox, rel)); err != nil {
				t.Errorf("the frame is not in the outbox, want it left there: %v", err)
			}
			if _, err := os.Stat(filepath.Join(w.archive, "astro", "M42_0001.fits")); !os.IsNotExist(err) {
				t.Errorf("Stat of the frame in the archive returned %v, want a not-exist error", err)
			}
		})
	}
}

// When the outbox and the archive are on the same claim and the file
// system refuses the rename, the frame stays in the outbox. Its catalog
// line is already written, so the next run renames the frame without
// writing a second line.
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
		t.Fatalf("creating the catalog directory: %v", err)
	}
	readOnly(t, archive)

	first, err := Run(options)
	if err != nil || len(first.Failures) != 1 || first.Failures[0].Reason != ReasonCopyFailed {
		t.Fatalf("the first run returned the report %+v and the error %v, want no error and 1 failure with the reason %s", first, err, ReasonCopyFailed)
	}
	if err := os.Chmod(archive, 0o755); err != nil {
		t.Fatalf("restoring the permissions of %s: %v", archive, err)
	}
	second, err := Run(options)

	_, counts := w.catalogLines()
	if err != nil || second.Promoted != 1 || counts[frameID] != 1 {
		t.Errorf("the second run returned the report %+v and the error %v, with %d catalog lines for the frame, want no error, 1 promoted frame, and 1 line", second, err, counts[frameID])
	}
	if _, err := os.Stat(filepath.Join(w.outbox, rel)); !os.IsNotExist(err) {
		t.Errorf("Stat of the frame in the outbox returned %v, want a not-exist error", err)
	}
}

// After a crash between the rename and the catalog line, a catalog
// that cannot be written leaves the frame in the outbox, so the frame
// is never deleted without its catalog line.
func TestAFrameWithNoLineIsNotDeleted(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through every permission")
	}
	w := newWorld(t)
	rel := w.expose("M42_0001", lightFrame(frameID))
	if _, err := Run(crashAt(w.options(), stepPlaced)); !errors.Is(err, errCrash) {
		t.Fatalf("the run that crashes at stepPlaced returned %v, want errCrash", err)
	}
	dir := filepath.Join(w.archive, "astro", catalog.Dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the catalog directory: %v", err)
	}
	readOnly(t, dir)

	report, err := Run(w.options())

	if err != nil || len(report.Failures) != 1 || report.Failures[0].Reason != ReasonCopyFailed {
		t.Errorf("Run returned the report %+v and the error %v, want no error and 1 failure with the reason %s", report, err, ReasonCopyFailed)
	}
	if _, err := os.Stat(filepath.Join(w.outbox, rel)); err != nil {
		t.Errorf("the frame is not in the outbox, want it left there: %v", err)
	}
}

// A directory of the Target's frames that cannot be read stops the run,
// so the Job's next pod tries again. The run does not treat the frames
// in that directory as deleted.
func TestADirectoryThatCannotBeReadStopsTheRun(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads through every permission")
	}
	w := newWorld(t)
	rel := w.expose("M42/2026-10-12/M42_0001", lightFrame(frameID))
	dir := filepath.Dir(filepath.Join(w.outbox, rel))
	if err := os.Chmod(dir, 0); err != nil {
		t.Fatalf("removing the permissions of %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if _, err := Run(w.options()); err == nil {
		t.Error("Run returned no error, want one for a directory that cannot be read")
	}
}

// A .part name that the run cannot remove beside an archived frame
// leaves the frame safe in the archive, so the run finishes the frame
// and logs the name that stays. The frame then leaves the outbox, so no
// later run visits it, and the name stays until a person removes it.
func TestAPartNameThatCannotBeRemovedDoesNotFailTheFrame(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes through every permission")
	}
	w := newWorld(t)
	frame := lightFrame(frameID)
	w.expose("M42_0001", frame)
	archived := filepath.Join(w.archive, "astro", "M42_0001.fits")
	w.write(archived, frame)
	w.write(filepath.Join(w.archive, "astro", "catalog", "m42.jsonl"), nil)
	if err := os.Link(archived, filepath.Join(w.archive, "astro", ".M42_0001.fits.part")); err != nil {
		t.Fatalf("linking the .part name: %v", err)
	}
	readOnly(t, filepath.Join(w.archive, "astro"))
	var log strings.Builder
	options := w.options()
	options.Log = &log

	report, err := Run(options)

	if err != nil || report.Promoted != 1 || len(report.Failures) != 0 {
		t.Errorf("Run returned the report %+v and the error %v, want one frame promoted and no failure", report, err)
	}
	if want := "promote: the hidden name .M42_0001.fits.part stays beside the archived frame until a person removes it: "; !strings.HasPrefix(log.String(), want) {
		t.Errorf("the log is %q, want a line that starts with %q, because no later run visits a frame that left the outbox", log.String(), want)
	}
}
