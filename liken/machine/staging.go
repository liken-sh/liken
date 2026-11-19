package machine

// A machine with a machineState storage role keeps each document it
// boots from in a directory on that filesystem. The documents are the
// Machine manifest, the Cluster manifest, the system release
// (systemrelease.go), the registry credentials (registries.go), and
// the imported images record (imports.go). Every directory holds the
// same files:
//
//	staged.yaml    a document waiting for its proving boot. The
//	               operator writes it when the cluster's copy differs
//	               from what this boot applied. init stages the
//	               imports record itself.
//	proven.yaml    the last document that booted successfully. A
//	               failed staged document falls back to it.
//	rejected.yaml  a staged document that failed its boot. init moves
//	               it aside so it never tries it again, and
//	               rejection.yaml records why.
//
// At boot, init uses the staged document when there is one, and the
// proven document otherwise. A successful boot promotes staged to
// proven with one rename. A failed boot rejects the staged document
// and boots the proven one. When neither staged.yaml nor proven.yaml
// exists, init boots the Machine and Cluster manifests from their seed
// copies in the image. A boot that had nothing staged writes its
// document with WriteProven once the boot is proven.
//
// A store reads and writes bytes and never parses a document. That is
// also why a rejection records the hash of the raw bytes: a document
// that does not parse still needs an identity.
//
// Every write is atomic and durable: write a temp file, fsync it,
// rename it into place, and fsync the directory. The rename alone is
// atomic against a crash of the writer. It does not survive power
// loss unless the directory is also synced, because the directory
// entry for the rename can be lost. These files must survive power
// loss, so they always do both fsyncs. The facts tree skips them
// because /run is tmpfs.

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

// MachineStateDir is where the machineState role mounts. The store
// constructors take the root as a parameter so tests can use a
// temporary directory, and the operator reads the same directories
// through this path.
const MachineStateDir = "/var/lib/liken/machine"

const (
	stagedManifest   = "staged.yaml"
	provenManifest   = "proven.yaml"
	rejectedManifest = "rejected.yaml"
	rejectionNote    = "rejection.yaml"
	attemptedMarker  = "attempted"
)

// A ManifestStore holds the staged, proven, and rejected files of one
// document, in one directory under machineState. Each document's
// constructor is the only place that names its directory.
type ManifestStore struct {
	dir string
}

// MachineManifests is the store of the Machine manifest under the
// machineState root.
func MachineManifests(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "manifests")}
}

// ClusterManifests is the store of the Cluster manifest under the
// machineState root.
func ClusterManifests(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "cluster")}
}

// A Rejection records why a staged document was refused. It is the
// schema of rejection.yaml and also the entry in the facts, so the
// console message, the file, and the cluster's status report the same
// record. The operator does not stage a document whose hash matches
// the rejection, so only a different edit clears the block.
type Rejection struct {
	Hash       string    `json:"hash,omitempty"`
	Reason     string    `json:"reason"`
	RejectedAt time.Time `json:"rejectedAt"`
}

// ManifestHash returns the sha256 of a document's bytes as they are in
// the file. The operator that stages a document and the init that
// boots or rejects it compare these hashes, so both must hash the
// same bytes.
func ManifestHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// renderDocument returns the bytes of a document that liken writes
// itself, and their hash. yaml marshals through JSON with sorted
// keys, so the same document always produces the same bytes and an
// unchanged document has an unchanged hash.
func renderDocument(doc any) ([]byte, string, error) {
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return nil, "", err
	}
	return raw, ManifestHash(raw), nil
}

// NewRejection builds the record of a refused document. It is separate
// from Reject because a boot reports the rejection on the console and
// in the facts even when writing the record to disk fails.
func NewRejection(raw []byte, reason string, at time.Time) Rejection {
	return Rejection{Hash: ManifestHash(raw), Reason: reason, RejectedAt: at}
}

// load reads one lifecycle file. It returns nil bytes and no error
// when the file does not exist, which is the usual state of a machine
// with nothing staged.
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

// LoadStaged returns the staged document, or nil when none is staged.
func (s ManifestStore) LoadStaged() ([]byte, error) {
	return s.load(stagedManifest)
}

// LoadProven returns the proven document, or nil when there is none.
func (s ManifestStore) LoadProven() ([]byte, error) {
	return s.load(provenManifest)
}

// LoadRejection reads rejection.yaml, or returns nil when there is
// none. The file stays until a later promotion or ClearRejection
// removes it. The facts are on tmpfs and a reboot loses them, so this
// file is what lets every boot keep reporting the rejection.
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

// WriteStaged writes the document for a boot to try.
func (s ManifestStore) WriteStaged(raw []byte) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return WriteDurable(filepath.Join(s.dir, stagedManifest), raw)
}

// WriteProven writes proven.yaml without a staged file to promote.
// init calls it for the seed Machine manifest, and the operator calls
// it for the seed Cluster manifest and for the system release that
// the machine runs.
func (s ManifestStore) WriteProven(raw []byte) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return WriteDurable(filepath.Join(s.dir, provenManifest), raw)
}

// Promote makes the staged document the proven one with one rename.
// The rename replaces the old proven file atomically, so a crash
// never leaves a machine with neither file. A success supersedes a
// rejection, so Promote then removes the rejection files and the
// attempted marker. A crash before that cleanup leaves a stale
// rejection note beside the new proven file. That is harmless,
// because the boot that just succeeded reports no rejection, and the
// next promotion removes the note.
func (s ManifestStore) Promote() error {
	if err := os.Rename(filepath.Join(s.dir, stagedManifest), filepath.Join(s.dir, provenManifest)); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(s.dir, rejectedManifest))
	_ = os.Remove(filepath.Join(s.dir, rejectionNote))
	_ = os.Remove(filepath.Join(s.dir, attemptedMarker))
	return syncDir(s.dir)
}

// Reject moves the staged document to rejected.yaml. It writes
// rejection.yaml durably first, then renames the staged file. A crash
// between the two steps leaves staged.yaml in place, so the next boot
// tries it again. That boot fails again for a persistent fault and
// repeats Reject, and succeeds for a transient one such as an
// unplugged disk. Either way the store reaches a consistent state.
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

// Init cannot tell at boot whether every staged document works. A bad
// Cluster document, for example, shows only later, as a machine that
// never joins the cluster. So init writes the attempted marker when it
// boots a staged document, and the component that can check the
// result promotes the document and removes the marker. For the Cluster document and the system release
// that component is the operator, because its pod runs only on a
// machine that joined the cluster. When init finds a marker that
// still matches the staged document, the previous boot was never
// proven, so init rejects the document and falls back to the proven
// one. A staged document gets one proving boot, so no retry counter
// is needed.

// WriteAttempted records the hash of the staged document that this
// boot is trying.
func (s ManifestStore) WriteAttempted(hash string) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	return WriteDurable(filepath.Join(s.dir, attemptedMarker), []byte(hash+"\n"))
}

// LoadAttempted returns the recorded hash, or an empty string when no
// trial is underway.
func (s ManifestStore) LoadAttempted() (string, error) {
	raw, err := s.load(attemptedMarker)
	if raw == nil || err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

// WithdrawStaged removes the staged document. The operator calls it
// when the cluster's copy has been edited back to what the machine
// already runs, so the next boot does not apply an edit that the
// cluster no longer declares. It also removes the attempted marker.
// The marker belongs to the withdrawn document, and if it stayed, the
// next staging of an identical document would look like a failed
// proving boot and be rejected. It is not an error when nothing is staged.
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

// ClearRejection removes rejected.yaml and rejection.yaml. The
// rejection exists to stop the operator from staging the same failed
// document again. Once the cluster no longer declares that document
// it has no use, and every boot would keep publishing it in the
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

// writeAtomic writes a file with a temp file and a rename, without
// fsync. The temp file is in the same directory because a rename
// cannot cross filesystems. A reader that polls sees either the old
// contents or the new, never a partial write. Files on tmpfs, such as
// the facts and the intent channel, use it because an fsync has
// nothing to flush.
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

// WriteDurable writes a file atomically and durably: a temp file in
// the same directory (a rename cannot cross filesystems), an fsync of
// the contents before the rename, and an fsync of the directory so
// the rename reaches disk. It is exported because init needs the same
// guarantee for the identity files that k3s reads, such as the node
// password.
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
