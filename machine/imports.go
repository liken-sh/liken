package machine

// The imported-images record is how a machine remembers which OS
// image tarballs its container store has proven it can serve.
//
// At startup, k3s's embedded containerd imports every OCI tarball in
// its agent/images directory: content and metadata land in its
// database, and each layer is extracted into a snapshot directory on
// clusterState. Those writes are not crash-ordered. A machine that
// dies at the wrong moment can be left with a database that says a
// layer is unpacked while the extracted files are torn, and because
// containerd trusts its own record, the same digest is never
// unpacked again: every container from that image fails with "exec
// format error", on every later boot, forever. When the torn image
// is the machine operator's, the machine has lost the program that
// would have reported the problem.
//
// The fix is not inside containerd, whose unpack cannot be made
// transactional from outside. Instead the imports ride the same
// staged/proven lifecycle as every liken document (staging.go), with
// this record as the document: the tarballs' digests, staged before
// k3s first sees them, proven once the operator observes the images
// actually serving containers. A boot that finds a staged record
// still standing knows the store took writes that were never proven
// and discards it wholesale rather than trusting it (init's
// imports.go carries that half).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// K3sAgentDir is the tree this record vouches for: k3s's agent
// state, where containerd keeps its store and the tarballs arrive
// (in its images/ subdirectory). One spelling, shared by both halves
// of the protocol, because their safety depends on agreeing: init
// discards this tree when a trial died unproven, and the operator's
// promotion barrier flushes exactly this filesystem.
const K3sAgentDir = "/var/lib/rancher/k3s/agent"

// ImportedImages lists the OS image tarballs one boot handed to
// containerd, each under the sha256 of its bytes. The digests are of
// the tarballs, not of the OCI images inside them: the record's job
// is to notice that the boot brought different bytes to import, and
// the tarball is the unit that arrives.
type ImportedImages struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`

	// Images maps each tarball's basename to the sha256 of its
	// contents. Empty on machines whose image carries no tarballs.
	Images map[string]string `json:"images,omitempty"`
}

// ImportedImagesStore is the record's lifecycle store under the given
// machineState root, beside the other documents'.
func ImportedImagesStore(root string) ManifestStore {
	return ManifestStore{dir: filepath.Join(root, "imports")}
}

// RenderImportedImages produces the record's canonical bytes and
// their hash. yaml marshals through JSON with sorted keys, so the
// same tarballs always render the same bytes, which is what lets a
// hash comparison answer "did anything change".
func RenderImportedImages(images map[string]string) ([]byte, string, error) {
	record := ImportedImages{
		APIVersion: APIVersion,
		Kind:       "ImportedImages",
		Images:     images,
	}
	raw, err := yaml.Marshal(&record)
	if err != nil {
		return nil, "", err
	}
	return raw, ManifestHash(raw), nil
}

// ParseImportedImages reads a record strictly, like every liken
// document: bytes that don't parse cleanly are refused rather than
// guessed at.
func ParseImportedImages(raw []byte) (*ImportedImages, error) {
	record := &ImportedImages{}
	if err := yaml.UnmarshalStrict(raw, record); err != nil {
		return nil, err
	}
	if record.Kind != "ImportedImages" {
		return nil, fmt.Errorf("expected kind ImportedImages, got %q", record.Kind)
	}
	return record, nil
}

// HashImageTarballs digests every .tar file in a directory, keyed by
// basename. A missing directory means no tarballs (an image without
// k3s), which is a normal answer, not an error.
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
