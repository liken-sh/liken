package machine

// The manifest lifecycle is how a document edit survives a reboot.
//
// A machine with a machineState storage role keeps its manifests on
// that filesystem, one directory per document. Several documents ride
// this lifecycle — each has a constructor below or in its own file
// (systemrelease.go, registries.go, imports.go) naming its directory —
// and every directory holds the same three files with the same
// meanings:
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
// A store deals in bytes, never parsed documents. The lifecycle's
// whole job is that the right bytes survive reboots and power loss;
// which kind those bytes are (a Machine, a Cluster) is for the caller
// to know. That is also why rejections hash raw bytes: a document
// that won't even parse must still be identifiable as exactly the
// bytes that were refused.
//
// Every write here is both atomic and durable: temp file, fsync the
// file, rename, fsync the directory. Rename alone makes a write
// atomic against crashes of the writer, but not against power loss:
// the rename itself lives in the directory, and an unsynced directory
// update can vanish with the power. Facts skip the fsyncs because
// /run is tmpfs; these files exist precisely to survive the power
// going out, so they do both fsyncs every time.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
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

// A ManifestStore is one document's lifecycle storage: a directory on
// machineState holding that document's staged, proven, and rejected
// files. Each document's constructor is the only place its directory
// is named, so no two lifecycles can ever collide.
type ManifestStore struct {
	dir string
}

// MachineManifests is the Machine manifest's store under the given
// machineState root.
func MachineManifests(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "manifests")}
}

// ClusterManifests is the Cluster manifest's store under the same
// root. It runs the same lifecycle as the Machine's, in its own
// directory.
func ClusterManifests(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "cluster")}
}

// A Rejection records why a staged document was refused. One type
// serves as both rejection.yaml's schema and the facts entry, so the
// console message, the on-disk record, and the cluster's status all
// carry the same record. The hash identifies exactly which bytes were
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

// renderDocument produces a document's canonical bytes and their
// hash, for the documents liken itself authors (the credentials, the
// imported-images record, the system release). yaml marshals through
// JSON with sorted keys, so the same document always renders the same
// bytes, which is what lets a hash comparison answer "did anything
// change".
func renderDocument(doc any) ([]byte, string, error) {
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return nil, "", err
	}
	return raw, ManifestHash(raw), nil
}

// NewRejection builds the record for refusing a staged document:
// exactly these bytes, for this reason, at this moment. It exists
// apart from Reject because a rejection is reported (on the console,
// in the boot's facts) even when recording it durably fails; the
// record's existence must not depend on the write succeeding.
func NewRejection(raw []byte, reason string, at time.Time) Rejection {
	return Rejection{Hash: ManifestHash(raw), Reason: reason, RejectedAt: at}
}

// load reads one lifecycle file. A missing file is not an error; it
// returns nil bytes, because most machines have nothing staged most
// of the time.
func (s ManifestStore) load(name string) ([]byte, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
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

// LoadRejection reads the standing rejection, if any. A rejection
// stands until a later promotion removes it. The facts file lives on
// tmpfs and is lost at every reboot, so this file is the durable
// record that lets every boot keep reporting "this document was
// rejected" until something supersedes it.
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
	return WriteDurable(filepath.Join(s.dir, stagedManifest), raw)
}

// WriteProven records a document as the last-known-good directly: the
// first successful boot does this with the image's seed, which is
// what closes the loop for a machine that has never had a staged
// document to promote.
func (s ManifestStore) WriteProven(raw []byte) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return WriteDurable(filepath.Join(s.dir, provenManifest), raw)
}

// Promote marks the staged document proven with one rename. The
// rename is atomic by the filesystem's own guarantee and replaces the
// old proven file in the same step, so there is never a moment with
// neither. A success supersedes
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

// Reject quarantines the staged document: the note is written durably
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
	if err := WriteDurable(filepath.Join(s.dir, rejectionNote), note); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(s.dir, stagedManifest), filepath.Join(s.dir, rejectedManifest)); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(s.dir, attemptedMarker))
	return syncDir(s.dir)
}

// The attempted marker gives a verdict to a trial that could not
// reach one on its own. Some documents can't be proven by the boot
// that runs them; a cluster document's failure modes show up
// downstream, since a bad endpoint just means the machine never
// joins. So init marks the staged document attempted when it boots
// it, and the component that can observe the proof promotes the
// document and clears the marker. For the cluster document that
// component is the operator, whose existence as a pod demonstrates
// the join. A boot that finds the marker still matching the staged
// document knows the last try was never proven, so it rejects the
// document and falls back. Each staged document gets exactly one
// proving boot, no retry counters are needed, and a crash at any
// point leaves a state the next boot reads correctly.

// WriteAttempted marks the staged document as being tried by this
// boot, identified by its hash.
func (s ManifestStore) WriteAttempted(hash string) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return WriteDurable(filepath.Join(s.dir, attemptedMarker), []byte(hash+"\n"))
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
// would apply an edit the cluster no longer asks for. The attempted
// marker goes with it: it described a trial of the withdrawn
// document, and if it were left behind, it would read as "tried and
// failed" the next time the identical document is staged. That would
// be a false rejection, because the trial never reached a verdict. A
// missing file is not an error; there is nothing to withdraw.
func (s ManifestStore) WithdrawStaged() error {
	if err := os.Remove(filepath.Join(s.dir, stagedManifest)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	_ = os.Remove(filepath.Join(s.dir, attemptedMarker))
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
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		removed = removed || err == nil
	}
	if !removed {
		return nil
	}
	return syncDir(s.dir)
}

// writeAtomic is the atomic write without the durability: temp file
// in the same directory (rename can't cross filesystems), then
// rename. Rename within a filesystem is atomic, so a reader polling
// on its own schedule sees either the old contents or the new, never
// a torn write. It is WriteDurable's sibling for files on tmpfs (the
// facts, the intent channel), where an fsync would have nothing to
// flush to.
func writeAtomic(path string, raw []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".liken-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// WriteDurable is the atomic, power-loss-proof write: temp file in
// the same directory (rename can't cross filesystems), contents
// fsynced before the rename makes them visible, directory fsynced so
// the rename itself is on disk. Exported because init needs the same
// guarantee for the identity files k3s reads (the node password).
func WriteDurable(path string, raw []byte) error {
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
