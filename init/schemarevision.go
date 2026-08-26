package main

// A boot seeds k3s's manifests from its own image, and the image is
// sometimes older than the cluster it boots: a fallback to the
// previous slot is the designed answer to a release that fails. For
// most manifests an older copy is merely stale. A CRD is different.
// When an older schema replaces a newer one, the API server keeps
// serving the stored objects, but it prunes the fields the older
// schema does not declare, and the next write persists the pruned
// object. No error is reported, and the object's generation does not
// change, so nothing that watches the object learns that data was
// lost.
//
// This file is the guard against that path. Each CRD manifest carries
// a revision annotation, the seed compares the image's copy against
// the copy already on the disk, and the disk's copy stays when it
// carries the higher revision. The guard runs in the image doing the
// seeding, so it protects a cluster only once every slot a machine
// can fall back to carries it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"sigs.k8s.io/yaml"
)

// schemaRevisionAnnotation names the integer that orders a CRD's
// schemas across releases. Every change to the schema raises it, and
// the pin test in schemarevision_test.go fails any change that does
// not, so the annotation cannot silently fall behind the schema it
// orders.
const schemaRevisionAnnotation = "liken.sh/schema-revision"

// manifestSizeLimit caps what the guard will read into memory. The
// reader runs in PID 1 on a machine that can boot with 1GB, and the
// largest seeded CRD is 178KB, so one megabyte is generous headroom
// rather than a constraint anyone meets.
const manifestSizeLimit = 1 << 20

// documentSeparator matches a YAML document separator on its own
// line, with the trailing spaces and the carriage return that a file
// written on another system carries.
var documentSeparator = regexp.MustCompile(`(?m)^---[ \t]*\r?$`)

// yamlDocuments splits a manifest into its documents and drops the
// empty ones, which a file that opens or closes with a separator
// leaves behind.
func yamlDocuments(raw []byte) [][]byte {
	var documents [][]byte
	for _, doc := range documentSeparator.Split(string(raw), -1) {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		documents = append(documents, []byte(doc))
	}
	return documents
}

// schemaRevision is what one manifest declares about its schema: this
// is a CustomResourceDefinition, this is the revision it carries,
// and, when the annotation names no revision at all, this is what it
// said instead.
type schemaRevision struct {
	isCRD    bool
	revision int
	bad      string
}

// crdSchemaRevision reads what a manifest declares about its schema.
// A manifest with no annotation is revision 0, which is what every
// release from before the annotation existed effectively declares.
// Two rules cover a value that does not fit an int. A run of digits
// too long to hold means the highest revision there is: the author's
// intent is unambiguous, and reading it as 0 would be the silent
// downgrade this file exists to stop. Anything else names no revision
// at all; it compares as 0, and the caller reports it, so a corrupted
// annotation is recoverable by the next seed instead of pinning the
// file forever on a machine with no shell.
func crdSchemaRevision(raw []byte) (schemaRevision, error) {
	for _, doc := range yamlDocuments(raw) {
		body, err := yaml.YAMLToJSON(doc)
		if err != nil {
			return schemaRevision{}, fmt.Errorf("parsing a manifest document: %w", err)
		}
		var meta struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(body, &meta); err != nil {
			return schemaRevision{}, fmt.Errorf("reading a manifest document: %w", err)
		}
		if meta.Kind != "CustomResourceDefinition" {
			continue
		}
		declared := strings.TrimSpace(meta.Metadata.Annotations[schemaRevisionAnnotation])
		if declared == "" {
			return schemaRevision{isCRD: true}, nil
		}
		revision, err := strconv.Atoi(declared)
		if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(declared, "-") {
			return schemaRevision{isCRD: true, revision: math.MaxInt}, nil
		}
		if err != nil || revision < 0 {
			return schemaRevision{isCRD: true, bad: declared}, nil
		}
		return schemaRevision{isCRD: true, revision: revision}, nil
	}
	return schemaRevision{}, nil
}

// readManifest reads a file that is a manifest and nothing else.
// init is PID 1: a read of a fifo never returns, and a read of a
// device or of a file the size of the disk takes the memory the
// machine boots with.
func readManifest(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is %s, not a regular file", path, info.Mode().Type())
	}
	if info.Size() > manifestSizeLimit {
		return nil, fmt.Errorf("%s is %d bytes, past the %d-byte limit on a manifest",
			path, info.Size(), manifestSizeLimit)
	}
	return os.ReadFile(path)
}

// newerCRDsOnDisk names the manifests whose disk copy outranks the
// image's: the files that declare a CRD on both sides, with the
// higher schema revision on the disk. The disk got a higher revision
// from a newer release that ran here before this one, so replacing it
// would downgrade the schema the cluster already serves.
//
// Every failure to read or parse falls through to the ordinary
// refresh, and the image's copy wins. That direction is deliberate: a
// file this function cannot judge must not be able to stop a boot,
// and an unreadable disk copy protects nothing anyway. The console
// line is the record that the comparison did not happen.
func newerCRDsOnDisk(src, dst string) map[string]bool {
	keep := map[string]bool{}
	entries, err := os.ReadDir(src)
	if err != nil {
		fmt.Printf("liken: k3s: reading the image's manifests: %v; seeding them all\n", err)
		return keep
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, err := os.Lstat(filepath.Join(dst, name)); err != nil {
			continue // the disk holds no copy of this manifest to keep
		}
		onDisk, err := readManifest(filepath.Join(dst, name))
		if err != nil {
			fmt.Printf("liken: k3s: %v; %s cannot be compared, so the image's copy replaces the disk's\n", err, name)
			continue
		}
		incoming, err := readManifest(filepath.Join(src, name))
		if err != nil {
			fmt.Printf("liken: k3s: %v; %s cannot be compared, so the image's copy replaces the disk's\n", err, name)
			continue
		}
		disk, err := crdSchemaRevision(onDisk)
		if err != nil {
			fmt.Printf("liken: k3s: %s on the disk does not parse: %v; the image's copy replaces it\n", name, err)
			continue
		}
		image, err := crdSchemaRevision(incoming)
		if err != nil {
			fmt.Printf("liken: k3s: %s in the image does not parse: %v; the image's copy replaces it\n", name, err)
			continue
		}
		reportBadRevision(name, "on the disk", disk)
		reportBadRevision(name, "in the image", image)
		if !disk.isCRD || !image.isCRD || disk.revision <= image.revision {
			continue
		}
		fmt.Printf("liken: k3s: %s on the disk declares schema revision %d and this image carries %d; keeping the disk's copy\n",
			name, disk.revision, image.revision)
		keep[name] = true
	}
	return keep
}

// reportBadRevision names a manifest whose annotation names no
// revision. The file then compares as revision 0, which is what a
// manifest with no annotation at all compares as.
func reportBadRevision(name, where string, declared schemaRevision) {
	if declared.bad == "" {
		return
	}
	fmt.Printf("liken: k3s: %s %s declares %s: %q, which is no revision; it compares as 0\n",
		name, where, schemaRevisionAnnotation, declared.bad)
}

// refreshSeedDir replaces a seed directory's content entry by entry,
// skipping the kept names on both the remove side and the copy side.
// The alternative, a wipe of the directory and a copy with the kept
// files written back, holds the kept content only in memory for a
// moment, and a power cut in that moment leaves the disk with the
// older schema and no record that a newer one existed. Entry by
// entry, the kept manifest is never removed and never rewritten, so
// no instant of the boot leaves the disk without the schema the
// cluster serves.
func refreshSeedDir(dst, src string, keep map[string]bool) error {
	existing, err := os.ReadDir(dst)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, entry := range existing {
		if keep[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dst, entry.Name())); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if keep[entry.Name()] {
			continue
		}
		from, to := filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.CopyFS(to, os.DirFS(from)); err != nil {
				return err
			}
			continue
		}
		if err := copySeedFile(to, from); err != nil {
			return err
		}
	}
	return nil
}

// copySeedFile copies one seed file, streaming it rather than holding
// it in memory: the operator images beside the manifests are tens of
// megabytes on a machine that boots with 1GB.
func copySeedFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is %s, not a regular file", src, info.Mode().Type())
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o666|info.Mode()&0o777)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
