package machine

// The imported-images record lists the OS image tarballs that a boot
// gave to containerd, so a later boot can tell whether the container
// store has served them.
//
// At startup, the containerd embedded in k3s imports every tarball in
// agent/images. It writes metadata to its database and extracts each
// layer into a snapshot directory, and it does not order these writes
// to survive a power loss. After a power loss, the database can record
// a layer as unpacked while its files are incomplete. containerd does
// not unpack that layer again, so every container from the image fails
// with "exec format error" on every later boot. When the damaged image
// is machine-operator's, nothing is left to report the fault.
//
// So the record goes through the staged and proven lifecycle of
// staging.go. init stages the record before k3s starts, and the
// operator promotes it once the images run containers. When a boot
// finds a staged record, the previous import may be incomplete, so
// init discards the whole store (init/imports.go).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/liken/api"
)

// K3sAgentDir is the k3s agent directory, which holds containerd's
// store and, in images/, the image tarballs. init discards it after an
// import that was never proven, and the operator calls syncfs on its
// filesystem before it promotes the record.
const K3sAgentDir = "/var/lib/rancher/k3s/agent"

// ImportedImages is the record. Its digests are of the tarball files,
// not the images inside them, because a changed tarball is the change
// that needs a new import.
type ImportedImages struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`

	// Images maps each tarball's file name to its sha256.
	Images map[string]string `json:"images,omitempty"`
}

// ImportedImagesStore returns the store in root/imports.
func ImportedImagesStore(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "imports")}
}

// RenderImportedImages returns the record's canonical bytes and their
// hash. init compares the hash with the proven record's hash, and
// stages a new record when they differ.
func RenderImportedImages(images map[string]string) ([]byte, string, error) {
	return renderDocument(ImportedImages{
		APIVersion: api.APIVersion,
		Kind:       "ImportedImages",
		Images:     images,
	})
}

// HashImageTarballs returns the sha256 of every .tar file in dir,
// keyed by file name. A missing directory returns nil and no error,
// because an OS image without k3s has no tarballs.
func HashImageTarballs(dir string) (map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	digests := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tar") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		sum := sha256.New()
		_, err = io.Copy(sum, f)
		f.Close()
		if err != nil {
			return nil, err
		}
		digests[entry.Name()] = hex.EncodeToString(sum.Sum(nil))
	}
	return digests, nil
}
