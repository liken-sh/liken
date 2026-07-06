package machine

// The manifest lifecycle: how a document edit survives a reboot.
//
// A machine with a machineState storage role keeps its manifests on
// that filesystem, one directory per document. Two documents ride
// this lifecycle today — the Machine manifest (manifests/) and the
// Cluster manifest (cluster/) — and each directory holds the same
// three files, telling the same story:
//
//	staged.yaml    a document awaiting its first successful boot,
//	               written by the operator when the cluster's copy
//	               drifts from what this boot actuated
//	proven.yaml    the last document that fully proved out: the
//	               last-known-good a failed staged document falls
//	               back to
//	rejected.yaml  a staged document that failed its boot, moved
//	               aside (with rejection.yaml saying why) so it is
//	               never tried again, but never silently forgotten
//	               either
//
// At boot, init prefers staged over proven; success promotes staged
// to proven (one rename), failure quarantines it and boots the proven
// document instead. The image's baked-in copy participates only when
// none of these files exist: it seeds the very first boot, and the
// first success writes it down as the first proven.yaml.
//
// A store deals in bytes, never parsed documents: the lifecycle's
// whole job is that the right bytes survive reboots and power loss,
// and which kind those bytes are (a Machine, a Cluster) is the
// caller's business. That's also why rejections hash raw bytes — a
// document that won't even parse must still be identifiable as
// exactly the bytes that were refused.
//
// Every write here is atomic *and* durable: temp file, fsync the
// file, rename, fsync the directory. Rename alone makes a write
// atomic against crashes of the writer, but not against power loss:
// the rename itself lives in the directory, and an unsynced directory
// update can vanish with the power. Facts skip the fsyncs because
// /run is tmpfs; these files exist precisely to survive the power
// going out, so they do both fsyncs every time.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sigs.k8s.io/yaml"
)

// MachineStateDir is where the machineState role mounts: the root the
// store constructors take as a parameter (so tests use a tempdir),
// and the path the operator reaches the same trees through.
const MachineStateDir = "/var/lib/liken/machine"

const (
	stagedManifest   = "staged.yaml"
	provenManifest   = "proven.yaml"
	rejectedManifest = "rejected.yaml"
	rejectionNote    = "rejection.yaml"
	attemptedMarker  = "attempted"
)

// A ManifestStore is one document's lifecycle home: a directory on
// machineState holding that document's staged, proven, and rejected
// files. The two constructors below are the only places the
// directories are named, so the Machine's and the Cluster's
// lifecycles can never collide.
type ManifestStore struct {
	dir string
}

// MachineManifests is the Machine manifest's store under the given
// machineState root.
func MachineManifests(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "manifests")}
}

// ClusterManifests is the Cluster manifest's store under the same
// root: the same lifecycle beside the Machine's, never entangled
// with it.
func ClusterManifests(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "cluster")}
}

// A Rejection records why a staged document was refused. One type
// serves as both rejection.yaml's schema and the facts entry, so what
// the console said, what the disk remembers, and what the cluster
// sees are one record. The hash identifies exactly which bytes were
// rejected: the operator refuses to re-stage a document matching it,
// and only a genuinely different edit clears the block.
type Rejection struct {
	Hash       string    `json:"hash,omitempty"`
	Reason     string    `json:"reason"`
	RejectedAt time.Time `json:"rejectedAt"`
}

// ManifestHash is a document's identity: the sha256 of its exact
// bytes as they sit in the file, because "the same document" must
// mean the same thing to the operator that staged it and the init
// that booted (or rejected) it.
func ManifestHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// load reads one lifecycle file. A missing file is not an error, it's
// an absence (nil bytes): most machines most of the time have nothing
// staged.
func (s ManifestStore) load(name string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func (s ManifestStore) LoadStaged() ([]byte, error) {
	return s.load(stagedManifest)
}

func (s ManifestStore) LoadProven() ([]byte, error) {
	return s.load(provenManifest)
}

// LoadRejection reads the standing rejection, if any. It stands until
// a later promotion removes it: facts live on tmpfs and die with
// every boot, so this file is the durable memory that lets every boot
// keep reporting "this document was rejected" until something
// supersedes it.
func (s ManifestStore) LoadRejection() (*Rejection, error) {
	raw, err := s.load(rejectionNote)
	if raw == nil || err != nil {
		return nil, err
	}
	r := &Rejection{}
	if err := yaml.UnmarshalStrict(raw, r); err != nil {
		return nil, err
	}
	return r, nil
}

// WriteStaged stages a document for the next boot: the operator's one
// write into the lifecycle.
func (s ManifestStore) WriteStaged(raw []byte) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return writeDurable(filepath.Join(s.dir, stagedManifest), raw)
}

// WriteProven records a document as the last-known-good directly: the
// first successful boot does this with the image's seed, which is
// what closes the loop for a machine that has never had a staged
// document to promote.
func (s ManifestStore) WriteProven(raw []byte) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return writeDurable(filepath.Join(s.dir, provenManifest), raw)
}

// Promote marks the staged document proven: one rename, atomic by the
// filesystem's own guarantee, replacing the old proven in the same
// motion (there is never a moment with neither). A success supersedes
// any old rejection, so those files go too; a crash between the
// rename and that cleanup leaves a stale rejection note beside a
// newer proven, which is harmless (the boot that just succeeded
// reports no rejection) and cleaned by the next promotion.
func (s ManifestStore) Promote() error {
	if err := os.Rename(filepath.Join(s.dir, stagedManifest), filepath.Join(s.dir, provenManifest)); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(s.dir, rejectedManifest))
	_ = os.Remove(filepath.Join(s.dir, rejectionNote))
	_ = os.Remove(filepath.Join(s.dir, attemptedMarker))
	return syncDir(s.dir)
}

// Reject quarantines the staged document: the note lands durably
// first, then staged becomes rejected. A crash between the two leaves
// staged.yaml in place, and the next boot simply retries it: a
// persistent failure re-records this same rejection and finishes the
// rename, and a transient one (the disk was unplugged) gets a second
// chance. Either way the interrupted state converges on its own.
func (s ManifestStore) Reject(r Rejection) error {
	note, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	if err := writeDurable(filepath.Join(s.dir, rejectionNote), note); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(s.dir, stagedManifest), filepath.Join(s.dir, rejectedManifest)); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(s.dir, attemptedMarker))
	return syncDir(s.dir)
}

// The attempted marker is how a trial that never reaches its verdict
// still gets one. Some documents can't be proven by the boot that
// runs them (a cluster document's failure modes are downstream: a bad
// endpoint just means the machine never joins), so init marks the
// staged document attempted when it boots it, and whoever holds the
// proof — for the cluster document, the operator, whose existence as
// a pod demonstrates the join — promotes and clears the marker. A
// boot that finds the marker still matching the staged document knows
// the last try was never proven: reject and fall back. One proving
// boot, no counters, and a crash anywhere leaves a state the next
// boot reads correctly.

// WriteAttempted marks the staged document as being tried by this
// boot, identified by its hash.
func (s ManifestStore) WriteAttempted(hash string) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return writeDurable(filepath.Join(s.dir, attemptedMarker), []byte(hash+"\n"))
}

// LoadAttempted reads the marker; "" means no trial is underway.
func (s ManifestStore) LoadAttempted() (string, error) {
	raw, err := s.load(attemptedMarker)
	if raw == nil || err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// WithdrawStaged removes the staged document. The operator calls this
// when the cluster's copy has been edited back to what the machine is
// already running: if the staged file stayed behind, the next boot
// would apply an edit the cluster no longer asks for. A missing file
// is not an error; there is nothing to withdraw.
func (s ManifestStore) WithdrawStaged() error {
	if err := os.Remove(filepath.Join(s.dir, stagedManifest)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return syncDir(s.dir)
}

// ClearRejection removes the rejected document and its rejection
// note. The rejection's purpose is to stop the operator from staging
// the same failed document again; once the cluster stops asking for
// it, the record has no further use, and without this cleanup every
// future boot would keep republishing it in status.
func (s ManifestStore) ClearRejection() error {
	removed := false
	for _, name := range []string{rejectedManifest, rejectionNote} {
		err := os.Remove(filepath.Join(s.dir, name))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		removed = removed || err == nil
	}
	if !removed {
		return nil
	}
	return syncDir(s.dir)
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
