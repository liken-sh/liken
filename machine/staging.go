package machine

// The manifest lifecycle: how a spec edit survives a reboot.
//
// A machine with a machineState storage role keeps its manifests on
// that filesystem, under manifests/. Three files tell the whole
// story:
//
//	staged.yaml    a spec awaiting its first successful boot, written
//	               by the operator when the cluster's Machine drifts
//	               from what this boot actuated
//	proven.yaml    the last spec that fully reconciled: the
//	               last-known-good a failed staged manifest falls
//	               back to
//	rejected.yaml  a staged manifest that failed its boot, moved
//	               aside (with rejection.yaml saying why) so it is
//	               never tried again, but never silently forgotten
//	               either
//
// At boot, init prefers staged over proven; success promotes staged
// to proven (one rename), failure quarantines it and boots the proven
// spec instead. The image's baked-in manifest participates only when
// none of these files exist: it seeds the very first boot, and the
// first success writes it down as the first proven.yaml.
//
// Every write here is atomic *and* durable: temp file, fsync the
// file, rename, fsync the directory. Rename alone makes a write
// atomic against crashes of the writer, but not against power loss:
// the rename itself lives in the directory, and an unsynced directory
// update can vanish with the power. Facts skip the fsyncs because
// /run is tmpfs; these files exist precisely to survive the power
// going out, so they pay full price.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"sigs.k8s.io/yaml"
)

// MachineStateDir is where the machineState role mounts: the root
// every lifecycle function here takes as a parameter (so tests use a
// tempdir), and the path the operator reaches the same tree through.
const MachineStateDir = "/var/lib/liken/machine"

const (
	stagedManifest   = "staged.yaml"
	provenManifest   = "proven.yaml"
	rejectedManifest = "rejected.yaml"
	rejectionNote    = "rejection.yaml"
)

func manifestsDir(root string) string {
	return filepath.Join(root, "manifests")
}

// A Rejection records why a staged manifest was refused. One type
// serves as both rejection.yaml's schema and the facts entry, so what
// the console said, what the disk remembers, and what the cluster
// sees are one record. The hash identifies exactly which bytes were
// rejected: the operator refuses to re-stage a spec matching it, and
// only a genuinely different edit clears the block.
type Rejection struct {
	Hash       string    `json:"hash,omitempty"`
	Reason     string    `json:"reason"`
	RejectedAt time.Time `json:"rejectedAt"`
}

// ManifestHash is a manifest's identity: the sha256 of its exact
// bytes as they sit in the file, because "the same spec" must mean
// the same thing to the operator that staged it and the init that
// booted (or rejected) it.
func ManifestHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// loadManifest reads and parses one lifecycle file. A missing file is
// not an error, it's an absence (nil Machine, nil bytes); a file that
// won't parse returns its raw bytes anyway, because the caller needs
// their hash to reject exactly what it read.
func loadManifest(path string) (*Machine, []byte, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	m, err := Parse(raw)
	if err != nil {
		return nil, raw, fmt.Errorf("%s: %w", path, err)
	}
	return m, raw, nil
}

func LoadStaged(root string) (*Machine, []byte, error) {
	return loadManifest(filepath.Join(manifestsDir(root), stagedManifest))
}

func LoadProven(root string) (*Machine, []byte, error) {
	return loadManifest(filepath.Join(manifestsDir(root), provenManifest))
}

// LoadRejection reads the standing rejection, if any. It stands until
// a later promotion removes it: facts live on tmpfs and die with
// every boot, so this file is the durable memory that lets every boot
// keep reporting "this spec was rejected" until something supersedes
// it.
func LoadRejection(root string) (*Rejection, error) {
	raw, err := os.ReadFile(filepath.Join(manifestsDir(root), rejectionNote))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r := &Rejection{}
	if err := yaml.UnmarshalStrict(raw, r); err != nil {
		return nil, err
	}
	return r, nil
}

// WriteStaged stages a manifest for the next boot: the operator's one
// write into the lifecycle.
func WriteStaged(root string, raw []byte) error {
	if err := os.MkdirAll(manifestsDir(root), 0o755); err != nil {
		return err
	}
	return writeDurable(filepath.Join(manifestsDir(root), stagedManifest), raw)
}

// WriteProven records a manifest as the last-known-good directly: the
// first successful boot does this with the image's seed manifest,
// which is what closes the loop for a machine that has never had a
// staged manifest to promote.
func WriteProven(root string, raw []byte) error {
	if err := os.MkdirAll(manifestsDir(root), 0o755); err != nil {
		return err
	}
	return writeDurable(filepath.Join(manifestsDir(root), provenManifest), raw)
}

// Promote marks the staged manifest proven: one rename, atomic by the
// filesystem's own guarantee, replacing the old proven in the same
// motion (there is never a moment with neither). A success supersedes
// any old rejection, so those files go too; a crash between the
// rename and that cleanup leaves a stale rejection note beside a
// newer proven, which is harmless (the boot that just succeeded
// reports no rejection) and cleaned by the next promotion.
func Promote(root string) error {
	dir := manifestsDir(root)
	if err := os.Rename(filepath.Join(dir, stagedManifest), filepath.Join(dir, provenManifest)); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(dir, rejectedManifest))
	_ = os.Remove(filepath.Join(dir, rejectionNote))
	return syncDir(dir)
}

// Reject quarantines the staged manifest: the note lands durably
// first, then staged becomes rejected. A crash between the two leaves
// staged.yaml in place, and the next boot simply retries it: a
// persistent failure re-records this same rejection and finishes the
// rename; a transient one (the disk was unplugged) gets the second
// chance it deserves. Self-healing in both directions.
func Reject(root string, r Rejection) error {
	dir := manifestsDir(root)
	note, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	if err := writeDurable(filepath.Join(dir, rejectionNote), note); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(dir, stagedManifest), filepath.Join(dir, rejectedManifest)); err != nil {
		return err
	}
	return syncDir(dir)
}

// WithdrawStaged removes the staged manifest. The operator calls this
// when the spec has been edited back to what the machine is already
// running: if the staged file stayed behind, the next boot would
// apply an edit the cluster no longer asks for. A missing file is not
// an error; there is nothing to withdraw.
func WithdrawStaged(root string) error {
	dir := manifestsDir(root)
	if err := os.Remove(filepath.Join(dir, stagedManifest)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return syncDir(dir)
}

// ClearRejection removes the rejected manifest and its rejection
// note. The rejection's purpose is to stop the operator from staging
// the same failed spec again; once the cluster stops asking for that
// spec, the record has no further use, and without this cleanup every
// future boot would keep republishing it in status.
func ClearRejection(root string) error {
	dir := manifestsDir(root)
	removed := false
	for _, name := range []string{rejectedManifest, rejectionNote} {
		err := os.Remove(filepath.Join(dir, name))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		removed = removed || err == nil
	}
	if !removed {
		return nil
	}
	return syncDir(dir)
}

// writeDurable is the atomic, power-loss-proof write: temp file in
// the same directory (rename can't cross filesystems), contents
// fsynced before the rename makes them visible, directory fsynced so
// the rename itself is on disk.
func writeDurable(path string, raw []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".liken-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
