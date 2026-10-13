package promote

// One run of a promotion Job: every frame of one target in one outbox
// moves to the target's archive, with one catalog line each (plan 03).
//
// Each frame goes through the same steps: copy it to a hidden .part
// file beside its name in the archive, read the copy back and compare
// the hashes, give the copy the frame's name with a rename that never
// replaces a file, append its catalog line, and delete it from the
// outbox. Each step syncs what it wrote before the next starts. So a
// crash at any point leaves the frame in at least one verified place,
// and the next run reads what the crash left and finishes the work
// without a second copy or a second line:
//
//   - a .part file is deleted and the copy starts again;
//   - a file under the frame's name with no catalog line is hashed with
//     the frame, and when the two match, the run records and deletes;
//   - a catalog line with the frame still in the outbox checks the
//     archive's file against the recorded hash, and deletes the frame.
//
// When the archive is the outbox's own claim, a frame moves by one
// rename and no byte is copied. Its line is recorded first, because the
// rename takes the frame out of the outbox, and a run after a crash
// finds only the frames that are still there.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/astrophotography-operator/catalog"
	"github.com/liken-sh/liken/astrophotography-operator/outbox"
)

// Camera is what the catalog line records of the camera that took a
// frame: its telescope and its optical train. A frame's place in the
// outbox names the camera and the reservation, and the operator passes
// the rest.
type Camera struct {
	Telescope string `json:"telescope"`
	Train     string `json:"train,omitempty"`
}

// Options are what one run needs.
type Options struct {
	// Outbox and Archive are where the two claims are mounted. With
	// SameClaim they are the same directory.
	Outbox    string
	Archive   string
	SameClaim bool
	// Path is the target's archive.path, and Target its name.
	Path   string
	Target string
	// Cameras holds each camera's telescope and train, by the camera's
	// name.
	Cameras map[string]Camera

	// after runs when a frame finishes a step, with the step and the
	// file it wrote. A test returns an error from it to stop the run the
	// way a killed pod stops, or changes the file.
	after func(step, file string) error
}

// The steps of a frame, in order.
const (
	stepCopied   = "copied"
	stepVerified = "verified"
	stepPlaced   = "placed"
	stepRecorded = "recorded"
	stepDeleted  = "deleted"
)

// run is the state of one run.
type run struct {
	Options
	catalog     *catalog.Catalog
	catalogFile string
	report      Report
}

// Run promotes every frame of the target in the outbox, and answers the
// report. An error means the run stopped before it reached every frame,
// such as a catalog that cannot be read. A frame that fails is reported
// and does not stop the run.
func Run(o Options) (Report, error) {
	r := &run{Options: o, catalogFile: filepath.Join(o.Archive, catalog.Path(o.Path, o.Target))}
	var err error
	if r.catalog, err = catalog.ReadFile(r.catalogFile); err != nil {
		return Report{}, fmt.Errorf("reading the catalog: %w", err)
	}
	frames, err := r.frames()
	if err != nil {
		return Report{}, err
	}
	for _, rel := range frames {
		if err := r.frame(rel); err != nil {
			return Report{}, err
		}
	}
	r.report.Totals = r.catalog.Totals()
	return r.report, nil
}

// frames answers each .fits file in the target's directories of the
// outbox, relative to the outbox's root, sorted by name. A hidden file
// is not a frame.
func (r *run) frames() ([]string, error) {
	// A Target's name is a DNS subdomain, which holds no character of
	// a pattern, so the pattern is valid.
	dirs, _ := filepath.Glob(filepath.Join(r.Outbox, "*", "*", r.Target))
	var frames []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !strings.HasSuffix(name, outbox.Extension) {
				return err
			}
			rel, err := filepath.Rel(r.Outbox, name)
			frames = append(frames, rel)
			return err
		})
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(frames)
	return frames, nil
}

// frame promotes one frame. It answers an error only to stop the run.
func (r *run) frame(rel string) error {
	// The walk finds only files below a target's directory, so the
	// path parses.
	place, _ := outbox.Parse(rel)
	source := filepath.Join(r.Outbox, rel)
	line, err := readFrame(source)
	var cut incomplete
	switch {
	case errors.As(err, &cut):
		r.wait(rel, ReasonIncomplete, cut.Error())
		r.report.Left++
		return nil
	case err != nil:
		_ = r.fail(rel, ReasonCopyFailed, err)
		return nil
	case line.FrameID == "":
		r.wait(rel, ReasonNoFrameID, "")
		return nil
	}
	camera := r.Cameras[place.Camera]
	line.Target, line.Reservation, line.Camera = r.Target, place.Reservation, place.Camera
	line.Telescope, line.Train = camera.Telescope, camera.Train
	line.Path = place.Name + outbox.Extension
	f := &frameRun{run: r, rel: rel, source: source, line: line, target: filepath.Join(r.Archive, r.Path, line.Path)}
	if err := f.promote(); !errors.Is(err, errFrameFailed) {
		return err
	}
	return nil
}

// frameRun is one frame's way through the steps.
type frameRun struct {
	*run
	rel    string
	source string
	target string
	line   catalog.Line
}

func (f *frameRun) promote() error {
	recorded, inCatalog := f.catalog.Frame(f.line.FrameID)
	archived, err := hashIfExists(f.target)
	if err != nil {
		return f.fail(f.rel, ReasonCopyFailed, err)
	}
	switch {
	case archived != "" && inCatalog && archived != recorded.SHA256:
		return f.conflict()
	case archived != "":
		return f.finishPlaced(archived, inCatalog)
	case f.SameClaim:
		return f.rename(recorded, inCatalog)
	default:
		return f.copy(recorded, inCatalog)
	}
}

// finishPlaced finishes a frame whose name in the archive holds a file:
// a crash came after the rename. When the file is the frame, the run
// records it if the crash came before the line, and deletes the frame.
func (f *frameRun) finishPlaced(archived string, inCatalog bool) error {
	sum, size, err := hashFile(f.source)
	if err != nil {
		return f.fail(f.rel, ReasonCopyFailed, err)
	}
	if sum != archived {
		return f.conflict()
	}
	if !inCatalog {
		if err := f.record(sum, size); err != nil {
			return err
		}
	}
	return f.delete()
}

// copy copies the frame to a .part file, reads the copy back, gives it
// the frame's name, records it unless the catalog holds it, and
// deletes the frame.
func (f *frameRun) copy(recorded catalog.Line, inCatalog bool) error {
	part := filepath.Join(filepath.Dir(f.target), "."+filepath.Base(f.target)+".part")
	sum, size, err := copyFile(f.source, part)
	if err != nil {
		return f.fail(f.rel, ReasonCopyFailed, err)
	}
	f.report.Bytes += size
	if err := f.step(stepCopied, part); err != nil {
		return err
	}
	back, _, err := hashFile(part)
	if err != nil || back != sum || (inCatalog && sum != recorded.SHA256) {
		_ = os.Remove(part)
		return f.fail(f.rel, ReasonVerifyFailed, verifyError(err, back, sum, recorded))
	}
	if err := f.step(stepVerified, part); err != nil {
		return err
	}
	if err := f.place(part); err != nil {
		_ = os.Remove(part)
		return f.placeFailed(err)
	}
	if err := f.step(stepPlaced, f.target); err != nil {
		return err
	}
	if !inCatalog {
		if err := f.record(sum, size); err != nil {
			return err
		}
	}
	return f.delete()
}

// verifyError says why a frame failed to verify: the read-back failed,
// the copy differs from the frame, or the frame differs from its line.
func verifyError(err error, back, sum string, recorded catalog.Line) error {
	switch {
	case err != nil:
		return err
	case back != sum:
		return fmt.Errorf("the copy read back as SHA-256 %s, and the frame read as %s", back, sum)
	}
	return fmt.Errorf("the frame reads as SHA-256 %s, and its catalog line records %s", sum, recorded.SHA256)
}

// rename moves the frame within one claim. The line comes first,
// because the rename takes the frame out of the outbox.
func (f *frameRun) rename(recorded catalog.Line, inCatalog bool) error {
	sum, size, err := hashFile(f.source)
	if err != nil {
		return f.fail(f.rel, ReasonCopyFailed, err)
	}
	if inCatalog && sum != recorded.SHA256 {
		return f.fail(f.rel, ReasonVerifyFailed, verifyError(nil, sum, sum, recorded))
	}
	if !inCatalog {
		if err := f.record(sum, size); err != nil {
			return err
		}
	}
	if err := f.place(f.source); err != nil {
		return f.placeFailed(err)
	}
	f.report.Promoted++
	return f.step(stepPlaced, f.target)
}

// place gives a file the frame's name in the archive and syncs both
// directories.
func (f *frameRun) place(from string) error {
	if err := os.MkdirAll(filepath.Dir(f.target), 0o755); err != nil {
		return err
	}
	if err := renameNoReplace(from, f.target); err != nil {
		return err
	}
	return errors.Join(catalog.SyncDir(filepath.Dir(f.target)), catalog.SyncDir(filepath.Dir(from)))
}

func (f *frameRun) placeFailed(err error) error {
	if errors.Is(err, errNameTaken) {
		return f.conflict()
	}
	return f.fail(f.rel, ReasonCopyFailed, err)
}

// record appends the frame's line to the catalog.
func (f *frameRun) record(sum string, size int64) error {
	f.line.SHA256, f.line.SizeBytes = sum, size
	if err := catalog.Append(f.catalogFile, f.line); err != nil {
		return f.fail(f.rel, ReasonCopyFailed, fmt.Errorf("recording the frame in the catalog: %w", err))
	}
	f.catalog.Add(f.line)
	return f.step(stepRecorded, f.catalogFile)
}

// delete removes the frame from the outbox.
func (f *frameRun) delete() error {
	if err := os.Remove(f.source); err != nil {
		return f.fail(f.rel, ReasonCopyFailed, err)
	}
	if err := catalog.SyncDir(filepath.Dir(f.source)); err != nil {
		return f.fail(f.rel, ReasonCopyFailed, err)
	}
	f.report.Promoted++
	return f.step(stepDeleted, f.source)
}

func (f *frameRun) conflict() error {
	name := filepath.ToSlash(filepath.Join(f.Path, f.line.Path))
	return f.fail(f.rel, ReasonNameConflict, fmt.Errorf("%s in the archive is another file", name))
}

// step runs the test's after, if any.
func (r *run) step(name, file string) error {
	if r.after == nil {
		return nil
	}
	return r.after(name, file)
}

// wait reports a frame that stays in the outbox with no fault.
func (r *run) wait(rel, reason, message string) {
	r.report.Waiting = append(r.report.Waiting, Frame{File: rel, Reason: reason, Message: message})
}

// errFrameFailed ends the steps of one frame that failed, so no later
// step runs: a frame whose line failed to append is not deleted. The
// run goes on with the next frame (run.frame).
var errFrameFailed = errors.New("the frame failed")

// fail reports a frame that the run failed to promote, and answers
// errFrameFailed.
func (r *run) fail(rel, reason string, err error) error {
	r.report.Failures = append(r.report.Failures, Frame{File: rel, Reason: reason, Message: err.Error()})
	r.report.Left++
	return errFrameFailed
}

// copyFile copies a file to a new file, hashing the bytes as it reads
// them, and syncs the copy. The copy is created with O_EXCL after any
// earlier copy of the same name is removed, so a .part file that a
// crash left never mixes with this one.
func copyFile(from, to string) (string, int64, error) {
	source, err := os.Open(from)
	if err != nil {
		return "", 0, err
	}
	defer source.Close()
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return "", 0, err
	}
	if err := os.Remove(to); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", 0, err
	}
	copied, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", 0, err
	}
	hash := sha256.New()
	size, err := io.Copy(copied, io.TeeReader(source, hash))
	if err == nil {
		err = copied.Sync()
	}
	if closeErr := copied.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(to)
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// hashFile answers a file's SHA-256 and size.
func hashFile(name string) (string, int64, error) {
	file, err := os.Open(name)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

// hashIfExists answers a file's SHA-256, and "" when no file holds the
// name.
func hashIfExists(name string) (string, error) {
	sum, _, err := hashFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return sum, err
}
