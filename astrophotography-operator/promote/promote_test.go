package promote

// These tests run Run against an outbox and an archive in t.TempDir().
// After a crash at any step and a second run, the frame must be in the
// archive once, with one catalog line.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/liken-sh/liken/astrophotography-operator/catalog"
)

const frameID = "0199d3a0-0000-7000-8000-4f2a9c01d3e7"

func sha(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func TestAFrameMovesToTheArchiveWithItsLine(t *testing.T) {
	w := newWorld(t)
	frame := lightFrame(frameID)
	w.expose("M42/2026-10-12/Luminance/M42_Luminance_300s_4f2a9c01d3e7", frame)

	report, err := Run(w.options())

	if err != nil {
		t.Fatalf("running the promotion: %v", err)
	}
	wantFiles := map[string]string{"astro/M42/2026-10-12/Luminance/M42_Luminance_300s_4f2a9c01d3e7.fits": string(frame)}
	if got := w.files(filepath.Join(w.archive)); !reflect.DeepEqual(withoutCatalog(got), wantFiles) {
		t.Errorf("the archive holds %v, want only the frame", keys(got))
	}
	if got := w.files(w.outbox); len(got) != 0 {
		t.Errorf("the outbox holds %v, want no files", keys(got))
	}
	lines, _ := w.catalogLines()
	want := catalog.Line{
		Record: catalog.RecordFrame, FrameID: frameID,
		Target: "m42", Reservation: "east-tonight", Telescope: "east", Camera: "east-main", Train: "east-imaging",
		Path:      "M42/2026-10-12/Luminance/M42_Luminance_300s_4f2a9c01d3e7.fits",
		SizeBytes: int64(len(frame)), SHA256: sha(frame), StartTime: "2026-10-12T23:15:06.864Z",
		Settings: catalog.Settings{Filter: "Luminance", ExposureSeconds: 300, Binning: 1, Gain: number(100), Offset: number(10)},
	}
	if !reflect.DeepEqual(lines[frameID], want) {
		t.Errorf("the line is\n%+v\nwant\n%+v", lines[frameID], want)
	}
	wantReport := Report{Totals: []Total{{Settings: want.Settings, Frames: 1}}, Promoted: 1, Bytes: int64(len(frame))}
	if !reflect.DeepEqual(report, wantReport) {
		t.Errorf("the report is %+v, want %+v", report, wantReport)
	}
}

func withoutCatalog(files map[string]string) map[string]string {
	out := map[string]string{}
	for name, content := range files {
		if filepath.Dir(name) != filepath.Join("astro", catalog.Dir) {
			out[name] = content
		}
	}
	return out
}

func keys(files map[string]string) []string {
	var out []string
	for name := range files {
		out = append(out, name)
	}
	return out
}

var errCrash = errors.New("the pod was killed")

// crashAt returns options whose run stops after step, as it would when
// the pod is killed.
func crashAt(o Options, step string) Options {
	o.after = func(done string, _ string) error {
		if done == step {
			return errCrash
		}
		return nil
	}
	return o
}

func TestACrashAtEachStepThenARerunLosesNothingAndRecordsOnce(t *testing.T) {
	cases := []struct {
		step      string
		sameClaim bool
	}{
		{stepCopied, false},
		{stepVerified, false},
		{stepPlaced, false},
		{stepRecorded, false},
		{stepDeleted, false},
		{stepRecorded, true},
		{stepPlaced, true},
	}
	for _, c := range cases {
		name := c.step
		if c.sameClaim {
			name += " on the same claim"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t)
			options := w.options()
			if c.sameClaim {
				w.archive = w.outbox
				options = w.options()
				options.SameClaim = true
			}
			frame := lightFrame(frameID)
			w.expose("M42_0001", frame)

			if _, err := Run(crashAt(options, c.step)); !errors.Is(err, errCrash) {
				t.Fatalf("the first run returned %v, want the crash", err)
			}
			report, err := Run(options)

			if err != nil {
				t.Fatalf("running the promotion again: %v", err)
			}
			archived := w.files(filepath.Join(w.archive, "astro"))
			_, counts := w.catalogLines()
			if archived["M42_0001.fits"] != string(frame) || counts[frameID] != 1 || len(withoutCatalogDir(archived)) != 1 {
				t.Errorf("the archive holds %v and %d catalog lines of the frame, want the frame once and one line", keys(archived), counts[frameID])
			}
			if _, err := os.Stat(filepath.Join(w.outbox, "east-main", "east-tonight", "m42", "M42_0001.fits")); !os.IsNotExist(err) {
				t.Errorf("the frame is in the outbox, want it deleted: %v", err)
			}
			if report.Left != 0 || len(report.Totals) != 1 || report.Totals[0].Frames != 1 {
				t.Errorf("the report is %+v, want no frames left and one in the totals", report)
			}
		})
	}
}

func withoutCatalogDir(files map[string]string) map[string]string {
	out := map[string]string{}
	for name, content := range files {
		if filepath.Dir(name) != catalog.Dir {
			out[name] = content
		}
	}
	return out
}

func TestTheSameClaimRenames(t *testing.T) {
	w := newWorld(t)
	w.archive = w.outbox
	options := w.options()
	options.SameClaim = true
	frame := lightFrame(frameID)
	rel := w.expose("M42_0001", frame)
	before, err := os.Stat(filepath.Join(w.outbox, rel))
	if err != nil {
		t.Fatalf("reading the outbox file: %v", err)
	}

	report, err := Run(options)

	if err != nil {
		t.Fatalf("running the promotion: %v", err)
	}
	after, err := os.Stat(filepath.Join(w.outbox, "astro", "M42_0001.fits"))
	if err != nil || !os.SameFile(before, after) {
		t.Errorf("the archive's file is not the outbox file renamed, want the same file: %v", err)
	}
	if report.Promoted != 1 || report.Bytes != 0 {
		t.Errorf("the report is %+v, want one frame and no bytes copied", report)
	}
}

// When another file already has the frame's name in the archive, both
// files stay where they are, and the report lists a name conflict.
func TestAnArchiveNameInUseByAnotherFileIsAConflict(t *testing.T) {
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
			other := lightFrame("0199d3a0-0000-7000-8000-000000000999")
			w.write(filepath.Join(w.archive, "astro", "M42_0001.fits"), other)
			frame := lightFrame(frameID)
			rel := w.expose("M42_0001", frame)

			report, err := Run(options)

			if err != nil {
				t.Fatalf("running the promotion: %v", err)
			}
			wantFailures := []Frame{{File: rel, Reason: ReasonNameConflict,
				Message: "the archive already has a different file at astro/M42_0001.fits"}}
			if !reflect.DeepEqual(report.Failures, wantFailures) || report.Left != 1 {
				t.Errorf("the report is %+v, want one name conflict and one frame left", report)
			}
			if w.files(filepath.Join(w.archive, "astro"))["M42_0001.fits"] != string(other) {
				t.Error("the archive's file changed, want it unchanged")
			}
			if w.files(w.outbox)[rel] != string(frame) {
				t.Error("the frame left the outbox, want it kept")
			}
		})
	}
}

// When another file takes the frame's name during the copy, the rename
// does not replace it, and the frame stays in the outbox.
func TestAFileCreatedAtTheNameDuringTheCopyIsAConflict(t *testing.T) {
	w := newWorld(t)
	options := w.options()
	other := []byte("a file another writer put there")
	options.after = func(step, _ string) error {
		if step == stepVerified {
			w.write(filepath.Join(w.archive, "astro", "M42_0001.fits"), other)
		}
		return nil
	}
	rel := w.expose("M42_0001", lightFrame(frameID))

	report, err := Run(options)

	if err != nil || len(report.Failures) != 1 || report.Failures[0].Reason != ReasonNameConflict {
		t.Errorf("the report is %+v, %v, want one name conflict and no error", report, err)
	}
	if archived := w.files(filepath.Join(w.archive, "astro")); len(archived) != 1 || archived["M42_0001.fits"] != string(other) {
		t.Errorf("the archive holds %v, want only the other writer's file", keys(archived))
	}
	if _, err := os.Stat(filepath.Join(w.outbox, rel)); err != nil {
		t.Errorf("the frame left the outbox, want it kept: %v", err)
	}
}

// When the archived file changes after its catalog line is appended,
// its hash differs from the line, so the frame stays in the outbox.
func TestAnArchivedFileThatDiffersFromItsLineIsAConflict(t *testing.T) {
	w := newWorld(t)
	frame := lightFrame(frameID)
	rel := w.expose("M42_0001", frame)
	if _, err := Run(crashAt(w.options(), stepRecorded)); !errors.Is(err, errCrash) {
		t.Fatalf("running the first promotion: %v", err)
	}
	w.write(filepath.Join(w.archive, "astro", "M42_0001.fits"), []byte("edited"))

	report, err := Run(w.options())

	if err != nil || len(report.Failures) != 1 || report.Failures[0].Reason != ReasonNameConflict {
		t.Errorf("the report is %+v, %v, want one name conflict and no error", report, err)
	}
	if w.files(w.outbox)[rel] != string(frame) {
		t.Error("the frame left the outbox, want it kept")
	}
}

// A copy whose bytes change before it is read back is removed, and the
// frame stays in the outbox.
func TestACopyThatReadsBackDifferentlyFails(t *testing.T) {
	w := newWorld(t)
	options := w.options()
	options.after = func(step, part string) error {
		if step == stepCopied {
			return os.WriteFile(part, []byte("a bit flipped on the link"), 0o644)
		}
		return nil
	}
	frame := lightFrame(frameID)
	rel := w.expose("M42_0001", frame)

	report, err := Run(options)

	if err != nil {
		t.Fatalf("running the promotion: %v", err)
	}
	if len(report.Failures) != 1 || report.Failures[0].Reason != ReasonVerifyFailed || report.Left != 1 {
		t.Errorf("the report is %+v, want one verification failure and one frame left", report)
	}
	if archived := w.files(w.archive); len(archived) != 0 {
		t.Errorf("the archive holds %v, want no files", keys(archived))
	}
	if w.files(w.outbox)[rel] != string(frame) {
		t.Error("the frame left the outbox, want it kept")
	}
}

func TestACopyThatCannotBeReadBackFails(t *testing.T) {
	w := newWorld(t)
	options := w.options()
	options.after = func(step, part string) error {
		if step == stepCopied {
			return os.Remove(part)
		}
		return nil
	}
	rel := w.expose("M42_0001", lightFrame(frameID))

	report, err := Run(options)

	if err != nil || len(report.Failures) != 1 || report.Failures[0].Reason != ReasonVerifyFailed {
		t.Errorf("the report is %+v, %v, want one verification failure and no error", report, err)
	}
	if _, err := os.Stat(filepath.Join(w.outbox, rel)); err != nil {
		t.Errorf("the frame left the outbox, want it kept: %v", err)
	}
}

// A frame that is still being written, or was never finished, stays in
// the outbox and is listed as waiting. A file with no FRAMEID was not
// written by this operator. It waits too, but report.Left does not
// count it.
func TestFramesThatCannotMoveYetWait(t *testing.T) {
	w := newWorld(t)
	cut := w.expose("M42_0001", lightFrame(frameID)[:3000])
	headerCut := w.expose("M42_0003", lightFrame(frameID)[:100])
	foreign := w.expose("M42_0002", fitsFrame(""))
	w.write(filepath.Join(w.outbox, "east-main", "east-tonight", "m42", "notes.txt"), []byte("not a frame"))
	w.write(filepath.Join(w.outbox, "east-main", "east-tonight", "m43", "M43_0001.fits"), lightFrame("another target"))

	report, err := Run(w.options())

	if err != nil {
		t.Fatalf("running the promotion: %v", err)
	}
	want := []Frame{
		{File: cut, Reason: ReasonIncomplete, Message: "the file holds 3000 of 5760 bytes"},
		{File: foreign, Reason: ReasonNoFrameID},
		{File: headerCut, Reason: ReasonIncomplete, Message: "the file ends inside its header"},
	}
	if !reflect.DeepEqual(report.Waiting, want) || report.Left != 2 {
		t.Errorf("the report is %+v, want the waiting frames and two left", report)
	}
}

func TestTheTotalsCountTheWholeCatalog(t *testing.T) {
	w := newWorld(t)
	earlier := catalog.Line{Record: catalog.RecordFrame, FrameID: "earlier",
		Settings: catalog.Settings{Filter: "Luminance", ExposureSeconds: 300, Binning: 1, Gain: number(100), Offset: number(10)}}
	if err := catalog.Append(filepath.Join(w.archive, "astro", "catalog", "m42.jsonl"), earlier); err != nil {
		t.Fatalf("appending the earlier catalog line: %v", err)
	}
	w.expose("M42_0001", lightFrame(frameID))

	report, err := Run(w.options())

	if err != nil || len(report.Totals) != 1 || report.Totals[0].Frames != 2 || report.Promoted != 1 {
		t.Errorf("the report is %+v, %v, want two frames in the totals and one promoted", report, err)
	}
}

func TestACatalogThatCannotBeReadStopsTheRun(t *testing.T) {
	w := newWorld(t)
	if err := os.MkdirAll(filepath.Join(w.archive, "astro", "catalog", "m42.jsonl"), 0o755); err != nil {
		t.Fatalf("making the catalog path a directory: %v", err)
	}
	if _, err := Run(w.options()); err == nil {
		t.Error("the run returned no error, want one for the unreadable catalog")
	}
}
