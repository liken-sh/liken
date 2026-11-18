package main

// At every boot, seedClusterState copies the manifests in the OS image
// into k3s's auto-deploy directory on the disk. The image can be older
// than the cluster, because a machine whose new release fails falls
// back to the previous slot. An older copy of a CRD loses data: the API
// server prunes the fields of stored objects that the older schema does
// not declare, and the next write saves the pruned object. No error is
// reported and the generation does not change, so no watcher learns
// that data was lost.
//
// This file is the guard against that. Each CRD manifest declares a
// revision annotation, and the seed keeps the disk's copy when its
// revision is higher than the image's. The guard runs in the image that
// seeds, so it protects a machine only when every slot the machine can
// fall back to has the guard.

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

// schemaRevisionAnnotation holds the integer that orders a CRD's
// schemas across releases. Every schema change must raise it, and
// TestTheSeededCRDsPinTheirSchemaRevision fails when one does not.
const schemaRevisionAnnotation = "liken.sh/schema-revision"

// manifestSizeLimit caps how much of a manifest the guard reads into
// memory. init is PID 1 on machines that can boot with 1GB, and no
// seeded CRD comes near one megabyte.
const manifestSizeLimit = 1 << 20

// documentSeparator matches a YAML document separator line, with
// trailing spaces or the carriage return of a CRLF file.
var documentSeparator = regexp.MustCompile(`(?m)^---[ \t]*\r?$`)

// yamlDocuments splits a manifest into its documents, without the empty
// ones that a leading or trailing separator leaves.
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

// schemaRevision is what one manifest declares about its schema. isCRD
// is true when the manifest contains a CustomResourceDefinition. invalid
// is the annotation's text when that text is not a non-negative integer.
type schemaRevision struct {
	isCRD    bool
	revision int
	invalid  string
}

// crdSchemaRevision returns the schema revision of the first
// CustomResourceDefinition in a manifest. A CRD with no annotation has
// revision 0, like the manifests of releases from before the
// annotation.
//
// A positive number too large for an int reads as math.MaxInt, because
// reading it as 0 would allow the downgrade. Any other value that is not
// a non-negative integer is returned in invalid and compares as 0, so
// the next seed replaces the file with the image's copy. Comparing it
// as a high revision would keep a corrupt file on a machine that has no
// shell to repair it.
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
			return schemaRevision{}, fmt.Errorf("decoding the kind and annotations of a manifest document: %w", err)
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
			return schemaRevision{isCRD: true, invalid: declared}, nil
		}
		return schemaRevision{isCRD: true, revision: revision}, nil
	}
	return schemaRevision{}, nil
}

// readManifest reads a manifest file. It refuses a symlink, and
// anything else that is not a regular file of at most manifestSizeLimit
// bytes, because init is PID 1. A read of a fifo can block forever, and
// a read of a device or a huge file can use all of the machine's memory.
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

// newerCRDsOnDisk returns the names of the manifests that the seed
// must keep: those where both copies contain a CRD and the disk's copy
// has the higher schema revision. A newer release wrote that copy on an
// earlier boot, and replacing it would downgrade the schema that the
// cluster serves.
//
// When either copy cannot be read or parsed, the function prints a
// console line and does not keep the manifest, so the image's copy
// replaces it as usual. A file that cannot be compared must not stop a
// boot, and an unreadable disk copy has no schema to protect.
func newerCRDsOnDisk(src, dst string) map[string]bool {
	keep := map[string]bool{}
	entries, err := os.ReadDir(src)
	if err != nil {
		fmt.Printf("liken: k3s: reading the image's manifests: %v, so every image manifest is seeded\n", err)
		return keep
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, err := os.Lstat(filepath.Join(dst, name)); err != nil {
			continue // nothing on the disk to keep
		}
		onDisk, err := readManifest(filepath.Join(dst, name))
		if err != nil {
			fmt.Printf("liken: k3s: %v, so %s cannot be compared and the image's copy replaces the disk's\n", err, name)
			continue
		}
		incoming, err := readManifest(filepath.Join(src, name))
		if err != nil {
			fmt.Printf("liken: k3s: %v, so %s cannot be compared and the image's copy replaces the disk's\n", err, name)
			continue
		}
		disk, err := crdSchemaRevision(onDisk)
		if err != nil {
			fmt.Printf("liken: k3s: %s on the disk does not parse: %v, so the image's copy replaces it\n", name, err)
			continue
		}
		image, err := crdSchemaRevision(incoming)
		if err != nil {
			fmt.Printf("liken: k3s: %s in the image does not parse: %v, so the image's copy replaces it\n", name, err)
			continue
		}
		reportInvalidRevision(name, "on the disk", disk)
		reportInvalidRevision(name, "in the image", image)
		if !disk.isCRD || !image.isCRD || disk.revision <= image.revision {
			continue
		}
		fmt.Printf("liken: k3s: %s on the disk declares schema revision %d and the image has %d, so the disk's copy stays\n",
			name, disk.revision, image.revision)
		keep[name] = true
	}
	return keep
}

// reportInvalidRevision prints a console line for a manifest whose
// revision annotation is not a non-negative integer.
func reportInvalidRevision(name, where string, declared schemaRevision) {
	if declared.invalid == "" {
		return
	}
	fmt.Printf("liken: k3s: %s %s declares %s: %q, which is not a non-negative integer, so it compares as 0\n",
		name, where, schemaRevisionAnnotation, declared.invalid)
}

// refreshSeedDir makes dst match src one entry at a time, and never
// removes or rewrites the names in keep. Removing dst whole and writing
// the kept files back would leave a kept manifest only in memory for a
// moment, and a power cut then would leave the disk with the older
// schema.
//
// Each entry succeeds or fails on its own, and the function returns all
// the failures together. If it stopped at the first failure, the
// entries it had already removed would not be copied back. An entry
// that cannot be removed is also not copied, so no file is half
// replaced.
func refreshSeedDir(dst, src string, keep map[string]bool) error {
	var failures []string
	existing, err := os.ReadDir(dst)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	stuck := map[string]bool{}
	for _, entry := range existing {
		if keep[entry.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(dst, entry.Name())); err != nil {
			stuck[entry.Name()] = true
			failures = append(failures, err.Error())
		}
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return collectSeedFailures(append(failures, err.Error()))
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return collectSeedFailures(append(failures, err.Error()))
	}
	for _, entry := range entries {
		if keep[entry.Name()] || stuck[entry.Name()] {
			continue
		}
		from, to := filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.CopyFS(to, os.DirFS(from)); err != nil {
				failures = append(failures, err.Error())
			}
			continue
		}
		if err := copySeedFile(to, from); err != nil {
			failures = append(failures, err.Error())
		}
	}
	return collectSeedFailures(failures)
}

// collectSeedFailures joins the failure messages into one error. The
// error is one line, because the console prints one fact per line and
// every line starts with liken:.
func collectSeedFailures(failures []string) error {
	if len(failures) == 0 {
		return nil
	}
	return errors.New(strings.Join(failures, "; "))
}

// copySeedFile streams one regular file to dst, which must not exist.
// It does not read the file into memory, because refreshSeedDir also
// copies the operator image tarballs of k3s/agent/images, which are
// tens of megabytes, and the machine can boot with 1GB.
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
