package machine

// The manifest lifecycle is how a document edit survives a reboot.
//
// A machine with a machineState storage role keeps its manifests on
// that filesystem, one directory for each document. Several documents
// use this lifecycle. Each one has a constructor below, or in its own
// file (systemrelease.go, registries.go, imports.go) that names its
// directory. Every directory holds the same three files, with the
// same meanings:
//
//	staged.yaml    a document that awaits its first successful boot,
//	               written by the operator when the cluster's copy
//	               differs from what this boot actuated
//	proven.yaml    the last document that fully proved out: the
//	               last-known-good copy that a failed staged
//	               document falls back to
//	rejected.yaml  a staged document that failed its boot, moved
//	               aside (with rejection.yaml stating why) so that
//	               init never tries it again, but never silently
//	               forgets it either
//
// At boot, init prefers the staged document over the proven one.
// Success promotes staged to proven with one rename. Failure
// quarantines the staged document and boots the proven document
// instead. The image's baked-in copy takes part only when none of
// these files exist: it seeds the very first boot, and the first
// success writes it down as the first proven.yaml.
//
// A store deals in bytes, never in parsed documents. The lifecycle's
// whole job is to make the right bytes survive reboots and power
// loss. Which kind of document those bytes hold, a Machine or a
// Cluster, is for the caller to know. This is also why rejections
// hash the raw bytes: a document that will not even parse must still
// be identifiable as exactly the bytes that init refused.
//
// Every write here is both atomic and durable: a temp file, an fsync
// of the file, a rename, and an fsync of the directory. A rename
// alone makes a write atomic against a crash of the writer, but not
// against power loss. The rename itself lives in the directory, and
// an unsynced directory update can vanish when the power goes out.
// Facts skip the fsyncs, because /run is tmpfs. These lifecycle files
// exist precisely to survive a power loss, so they perform both
// fsyncs every time.

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

// MachineStateDir is where the machineState role mounts. It is the
// root that the store constructors take as a parameter, so tests can
// use a temporary directory, and it is the path through which the
// operator reaches the same trees.
const MachineStateDir = "/var/lib/liken/machine"

const (
	stagedManifest   = "staged.yaml"
	provenManifest   = "proven.yaml"
	rejectedManifest = "rejected.yaml"
	rejectionNote    = "rejection.yaml"
	attemptedMarker  = "attempted"
)

// A ManifestStore is one document's lifecycle storage: a directory on
// machineState that holds that document's staged, proven, and
// rejected files. Each document's constructor is the only place that
// names its directory, so no two lifecycles can ever collide.
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
// rejected. The operator refuses to re-stage a document that matches
// it, and only a genuinely different edit clears the block.
type Rejection struct {
	Hash       string    `json:"hash,omitempty"`
	Reason     string    `json:"reason"`
	RejectedAt time.Time `json:"rejectedAt"`
}

// ManifestHash is a document's identity: the sha256 of its exact
// bytes as they sit in the file. "The same document" must mean the
// same thing to the operator that staged it and to the init that
// booted it, or rejected it.
func ManifestHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// renderDocument produces a document's canonical bytes and their
// hash, for the documents that liken itself authors: the
// credentials, the imported-images record, and the system release.
// yaml marshals through JSON with sorted keys, so the same document
// always renders the same bytes. This is what lets a hash comparison
// answer the question "did anything change".
func renderDocument(doc any) ([]byte, string, error) {
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return nil, "", err
	}
	return raw, ManifestHash(raw), nil
}

// NewRejection builds the record for refusing a staged document:
// exactly these bytes, for this reason, at this moment. It exists
// apart from Reject, because a boot reports a rejection (on the
// console, in the boot's facts) even when the durable write of the
// record fails. The record's existence must not depend on the write
// succeeding.
func NewRejection(raw []byte, reason string, at time.Time) Rejection {
	return Rejection{Hash: ManifestHash(raw), Reason: reason, RejectedAt: at}
}

// load reads one lifecycle file. A missing file is not an error. It
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
// stands until a later promotion removes it. tmpfs holds the facts
// file, and each reboot loses it, so this file is the durable record
// that lets every boot keep reporting "this document was rejected"
// until something supersedes it.
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

// WriteProven records a document as the last-known-good copy
// directly. The first successful boot does this with the image's
// seed. This closes the loop for a machine that has never had a
// staged document to promote.
func (s ManifestStore) WriteProven(raw []byte) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return WriteDurable(filepath.Join(s.dir, provenManifest), raw)
}

// Promote marks the staged document proven with one rename. The
// rename is atomic by the filesystem's own guarantee, and it replaces
// the old proven file in the same step, so there is never a moment
// with neither file present. A success supersedes any old rejection,
// so those files go too. A crash between the rename and that cleanup
// leaves a stale rejection note beside a newer proven file. This is
// harmless: the boot that just succeeded reports no rejection, and
// the next promotion cleans up the stale note.
func (s ManifestStore) Promote() error {
	if err := os.Rename(filepath.Join(s.dir, stagedManifest), filepath.Join(s.dir, provenManifest)); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(s.dir, rejectedManifest))
	_ = os.Remove(filepath.Join(s.dir, rejectionNote))
	_ = os.Remove(filepath.Join(s.dir, attemptedMarker))
	return syncDir(s.dir)
}

// Reject quarantines the staged document. Reject writes the note
// durably first, then turns staged into rejected. A crash between the
// two steps leaves staged.yaml in place, and the next boot simply
// retries it. A persistent failure re-records this same rejection and
// finishes the rename. A transient failure, such as an unplugged
// disk, gets a second chance. Either way, the interrupted state
// converges on its own.
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
// reach one on its own. Some documents cannot be proven by the boot
// that runs them. A cluster document's failure modes show up
// downstream, because a bad endpoint just means the machine never
// joins the cluster. So init marks the staged document attempted when
// it boots the document, and the component that can observe the
// proof promotes the document and clears the marker. For the cluster
// document, that component is the operator, and the operator's
// existence as a pod demonstrates the join. A boot that finds the
// marker still matching the staged document knows the last try was
// never proven, so it rejects the document and falls back. Each
// staged document gets exactly one proving boot. No retry counters
// are needed, and a crash at any point leaves a state that the next
// boot reads correctly.

// WriteAttempted marks the staged document, identified by its hash,
// as tried by this boot.
func (s ManifestStore) WriteAttempted(hash string) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return WriteDurable(filepath.Join(s.dir, attemptedMarker), []byte(hash+"\n"))
}

// LoadAttempted reads the marker. An empty string means no trial is
// underway.
func (s ManifestStore) LoadAttempted() (string, error) {
	raw, err := s.load(attemptedMarker)
	if raw == nil || err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// WithdrawStaged removes the staged document. The operator calls this
// when someone has edited the cluster's copy back to what the machine
// already runs. If the staged file stayed behind, the next boot would
// apply an edit that the cluster no longer asks for. The attempted
// marker goes with it. The marker described a trial of the withdrawn
// document, and if it stayed behind, it would read as "tried and
// failed" the next time someone stages the identical document. That
// would be a false rejection, because the trial never reached a
// verdict. A missing file is not an error; there is nothing to
// withdraw.
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
// the same failed document again. Once the cluster stops asking for
// that document, the record has no further use. Without this
// cleanup, every future boot would keep republishing the record in
// status.
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

// writeAtomic performs the atomic write without the durability: a
// temp file in the same directory, because a rename cannot cross
// filesystems, followed by a rename. A rename within one filesystem
// is atomic, so a reader that polls on its own schedule sees either
// the old contents or the new, never a torn write. It is
// WriteDurable's sibling for files on tmpfs, such as the facts and
// the intent channel, where an fsync would have nothing to flush to.
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

// WriteDurable performs the atomic, power-loss-proof write: a temp
// file in the same directory, because a rename cannot cross
// filesystems; the contents fsynced before the rename makes them
// visible; and the directory fsynced so the rename itself reaches
// disk. It is exported because init needs the same guarantee for the
// identity files that k3s reads, such as the node password.
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
