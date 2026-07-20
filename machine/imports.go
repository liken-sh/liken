package machine

// The imported-images record stores which OS image tarballs a
// machine's container store has proven it can serve.
//
// At startup, k3s's embedded containerd imports every OCI tarball in
// its agent/images directory. Content and metadata land in its
// database, and containerd extracts each layer into a snapshot
// directory on clusterState. Those writes do not happen in a
// crash-safe order. A machine that dies at the wrong moment can be
// left with a database that says a layer is unpacked, while the
// extracted files are torn. containerd trusts its own record, so it
// never unpacks the same digest again. Every container from that
// image then fails with "exec format error", on every later boot,
// forever. If the torn image is the machine operator's image, the
// machine has lost the program that would have reported the
// problem.
//
// The fix does not sit inside containerd. containerd's unpack cannot
// become transactional from outside containerd. Instead, the imports
// go through the same staged and proven lifecycle as every liken
// document (staging.go), with this record as the document. The
// tarballs' digests are staged before k3s first sees them, and
// proven once the operator observes the images actually serving
// containers. If a boot finds a staged record still present, the
// boot knows the store took writes that were never proven. The boot
// discards the record completely, instead of trusting it. init's
// imports.go carries that half of the work.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/liken-sh/liken/api"
)

// K3sAgentDir names the tree this record covers: k3s's agent state,
// where containerd keeps its store and the tarballs arrive (in its
// images/ subdirectory). Both halves of the protocol share this one
// spelling, because their safety depends on agreement. init discards
// this tree when a trial died unproven. The operator's promotion
// barrier flushes exactly this filesystem.
const K3sAgentDir = "/var/lib/rancher/k3s/agent"

// ImportedImages lists the OS image tarballs that one boot handed to
// containerd, each keyed by the sha256 of its bytes. The digests
// cover the tarballs, not the OCI images inside them. The record
// exists to notice that the boot brought different bytes to import,
// and the tarball is the unit that arrives.
type ImportedImages struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`

	// Images maps each tarball's basename to the sha256 of its
	// contents. Images is empty on machines whose image carries no
	// tarballs.
	Images map[string]string `json:"images,omitempty"`
}

// ImportedImagesStore returns the record's lifecycle store under the
// given machineState root, beside the other documents' stores.
func ImportedImagesStore(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "imports")}
}

// RenderImportedImages produces the record's canonical bytes and
// their hash. A boot compares this hash against the proven record's
// hash, to decide whether the boot is bringing different tarballs to
// import.
func RenderImportedImages(images map[string]string) ([]byte, string, error) {
	return renderDocument(ImportedImages{
		APIVersion: api.APIVersion,
		Kind:       "ImportedImages",
		Images:     images,
	})
}

// HashImageTarballs digests every .tar file in a directory, keyed by
// basename. A missing directory means no tarballs are present, as
// happens with an image that has no k3s. HashImageTarballs treats a
// missing directory as a normal answer, not an error.
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
