package promote

// One run of a promotion Job moves every frame of one target from an
// outbox to the target's archive and appends one catalog line for each.
//
// For each frame, the run copies it to a hidden .part file next to its
// final name in the archive, reads the copy back and compares the
// hashes, renames the copy to the frame's name without replacing an
// existing file, appends the catalog line, and deletes the frame from
// the outbox. Each step syncs what it wrote before the next step
// starts, so after a crash the frame is whole in the outbox, the
// archive, or both. The next run finishes the frame without a second
// copy or a second catalog line:
//
//   - It deletes a .part file and copies again.
//   - When a file has the frame's name but no catalog line, it hashes
//     the file and compares it with the frame. When they match, it
//     appends the line and deletes the frame.
//   - When the catalog has a line and the frame is still in the outbox,
//     it compares the archive's file with the recorded hash and with
//     the frame, then deletes the frame.
//
// When the archive and the outbox are the same claim, the frame moves
// with one rename and no copy. The run appends the line before the
// rename, because a run after a crash finds only the frames that are
// still in the outbox.

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

// Camera is the telescope and optical train that the run writes into
// the catalog line of each frame from a camera. The frame's path gives
// only the camera's name.
type Camera struct {
	Telescope string `json:"telescope"`
	Train     string `json:"train,omitempty"`
}

// Options are the inputs of one run.
type Options struct {
	// Outbox and Archive are the mount paths of the two claims. With
	// SameClaim they are the same directory.
	Outbox    string
	Archive   string
	SameClaim bool
	// Path is the target's spec.archive.path, and Target is its name.
	Path    string
	Target  string
	Cameras map[string]Camera

	// after, when set, is called each time a frame finishes a step, with
	// the step and the file the step wrote. A test returns an error from
	// it to stop the run as a killed pod would, or changes the file.
	after func(step, file string) error
}

// The steps that after receives.
const (
	stepCopied   = "copied"
	stepVerified = "verified"
	stepPlaced   = "placed"
	stepRecorded = "recorded"
	stepDeleted  = "deleted"
)

// run is the state of one call to Run: the target's catalog, the file
// that holds it, and the report that Run returns.
type run struct {
	Options
	catalog     *catalog.Catalog
	catalogFile string
	report      Report
}

// Run promotes every frame of the target in the outbox. A frame that
// fails is listed in the report and does not stop the run. Run returns
// an error only when it cannot read the catalog or list the outbox, or
// when the after hook returns one.
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

// frames returns the sorted path, relative to the outbox, of each .fits
// file under the target's directories. It skips names that start with
// ".", which are not frames.
func (r *run) frames() ([]string, error) {
	// A Target name is a DNS subdomain, so it contains no glob
	// metacharacter and the pattern is always valid.
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

// frame promotes one frame. It returns an error only to stop the run.
func (r *run) frame(rel string) error {
	// frames returns only files below a target's directory, so the path
	// always parses.
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

// frameRun is the state of one frame as it goes through the steps.
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

// finishPlaced handles a frame whose name in the archive already has a
// file. When the file has the frame's hash, an earlier run crashed after
// the rename, so finishPlaced appends the catalog line if it is missing
// and deletes the frame. Otherwise it reports a name conflict.
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

func verifyError(err error, back, sum string, recorded catalog.Line) error {
	switch {
	case err != nil:
		return err
	case back != sum:
		return fmt.Errorf("the copy has SHA-256 %s, and the frame has %s", back, sum)
	}
	return fmt.Errorf("the frame has SHA-256 %s, and its catalog line records %s", sum, recorded.SHA256)
}

// rename moves the frame within one claim. The package comment gives
// the reason that it appends the catalog line first.
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

// place renames from to the frame's name in the archive. It returns
// errNameTaken instead of replacing an existing file.
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

func (f *frameRun) record(sum string, size int64) error {
	f.line.SHA256, f.line.SizeBytes = sum, size
	if err := catalog.Append(f.catalogFile, f.line); err != nil {
		return f.fail(f.rel, ReasonCopyFailed, fmt.Errorf("recording the frame in the catalog: %w", err))
	}
	f.catalog.Add(f.line)
	return f.step(stepRecorded, f.catalogFile)
}

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
	return f.fail(f.rel, ReasonNameConflict, fmt.Errorf("the archive already has a different file at %s", name))
}

func (r *run) step(name, file string) error {
	if r.after == nil {
		return nil
	}
	return r.after(name, file)
}

// wait lists a frame that stays in the outbox without a fault.
func (r *run) wait(rel, reason, message string) {
	r.report.Waiting = append(r.report.Waiting, Frame{File: rel, Reason: reason, Message: message})
}

// errFrameFailed stops the steps of a frame after fail has listed it, so
// that, for example, a frame whose catalog line failed to append is not
// deleted. run.frame turns it into nil and the run goes on to the next
// frame.
var errFrameFailed = errors.New("the frame is listed as failed in the report")

func (r *run) fail(rel, reason string, err error) error {
	r.report.Failures = append(r.report.Failures, Frame{File: rel, Reason: reason, Message: err.Error()})
	r.report.Left++
	return errFrameFailed
}

// copyFile copies from to to, syncs the copy, and returns the SHA-256 of
// the bytes it read and the size. It removes any earlier file at to and
// creates the new one with O_EXCL, so a .part file that a crash left
// never mixes with this copy.
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

// hashIfExists returns a file's SHA-256, or "" when the file does not
// exist.
func hashIfExists(name string) (string, error) {
	sum, _, err := hashFile(name)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	return sum, err
}
