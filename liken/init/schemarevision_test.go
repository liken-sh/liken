package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"sigs.k8s.io/yaml"
)

func crdManifest(revision string) []byte {
	return []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: machines.liken.sh
  annotations:
    liken.sh/schema-revision: "` + revision + `"
spec:
  group: liken.sh
`)
}

// unannotatedCRD is a CRD manifest with no revision annotation, like
// the manifests of releases from before the annotation.
var unannotatedCRD = []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: machines.liken.sh
spec:
  group: liken.sh
`)

func TestCRDSchemaRevisionReadsWhatAManifestDeclares(t *testing.T) {
	cases := []struct {
		name  string
		raw   []byte
		want  schemaRevision
		fails bool
	}{
		{"an annotated CRD", crdManifest("7"), schemaRevision{isCRD: true, revision: 7}, false},
		{"a padded revision", crdManifest("  7  "), schemaRevision{isCRD: true, revision: 7}, false},
		{"a CRD with no annotation", unannotatedCRD, schemaRevision{isCRD: true}, false},
		{"an empty revision", crdManifest(""), schemaRevision{isCRD: true}, false},
		{"a revision past the width of an int", crdManifest("99999999999999999999"),
			schemaRevision{isCRD: true, revision: math.MaxInt}, false},
		{"a revision that is not a number", crdManifest("two"),
			schemaRevision{isCRD: true, invalid: "two"}, false},
		{"a negative revision", crdManifest("-3"), schemaRevision{isCRD: true, invalid: "-3"}, false},
		{"a negative revision past the width of an int", crdManifest("-99999999999999999999"),
			schemaRevision{isCRD: true, invalid: "-99999999999999999999"}, false},
		{"a workload manifest", []byte("kind: DaemonSet\nmetadata:\n  name: liken\n"), schemaRevision{}, false},
		{"a CRD in the second document", append([]byte("kind: Namespace\n---\n"), crdManifest("4")...),
			schemaRevision{isCRD: true, revision: 4}, false},
		{"a separator with trailing space", append([]byte("kind: Namespace\n--- \n"), crdManifest("5")...),
			schemaRevision{isCRD: true, revision: 5}, false},
		{"a file with CRLF line endings",
			[]byte(strings.ReplaceAll("kind: Namespace\n---\n"+string(crdManifest("6")), "\n", "\r\n")),
			schemaRevision{isCRD: true, revision: 6}, false},
		{"a bare separator at the end", append(crdManifest("8"), []byte("---")...),
			schemaRevision{isCRD: true, revision: 8}, false},
		{"a manifest that does not parse", []byte("kind: [\n  unterminated\n"), schemaRevision{}, true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := crdSchemaRevision(test.raw)
			if (err != nil) != test.fails {
				t.Fatalf("the error is %v, want failure: %v", err, test.fails)
			}
			if got != test.want {
				t.Errorf("the revision is %+v, want %+v", got, test.want)
			}
		})
	}
}

// writeManifest writes one manifest file into dir, which it creates
// when needed, and returns the file's path.
func writeManifest(t *testing.T, dir, name string, raw []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating the manifest directory: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("writing the manifest: %v", err)
	}
	return path
}

// manifestRevision returns the schema revision of the manifest that
// the seed left on the disk.
func manifestRevision(t *testing.T, root, name string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, likenManifestsRel, name))
	if err != nil {
		t.Fatalf("reading the seeded manifest: %v", err)
	}
	got, err := crdSchemaRevision(raw)
	if err != nil {
		t.Fatalf("reading the schema revision: %v", err)
	}
	return got.revision
}

func seedOrFail(t *testing.T, root string) {
	t.Helper()
	if err := seedClusterState(root); err != nil {
		t.Fatalf("seeding the cluster state: %v", err)
	}
}

// A machine that falls back to the older slot boots an image with an
// older CRD manifest. If the seed copied it over the disk's copy, every
// later write would drop the fields that the older schema does not
// declare.
func TestSeedClusterStateKeepsANewerCRDFromTheDisk(t *testing.T) {
	cases := []struct {
		name     string
		onDisk   []byte
		image    []byte
		revision int
	}{
		{"an older image", crdManifest("2"), crdManifest("1"), 2},
		{"an image from before the annotation", crdManifest("1"), unannotatedCRD, 1},
		{"an image whose revision does not parse", crdManifest("1"), crdManifest("two"), 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			src := fakeSeedSource(t)
			root := t.TempDir()
			writeManifest(t, filepath.Join(src, likenManifestsRel), "machines-crd.yaml", test.image)
			writeManifest(t, filepath.Join(root, likenManifestsRel), "machines-crd.yaml", test.onDisk)

			seedOrFail(t, root)

			if revision := manifestRevision(t, root, "machines-crd.yaml"); revision != test.revision {
				t.Errorf("the disk keeps revision %d, want the newer %d", revision, test.revision)
			}
		})
	}
}

func TestSeedClusterStateCopiesTheImagesCRDWhenTheDiskIsNotNewer(t *testing.T) {
	cases := []struct {
		name     string
		onDisk   []byte
		image    []byte
		revision int
	}{
		{"an older disk", crdManifest("1"), crdManifest("2"), 2},
		{"an equal disk", crdManifest("2"), crdManifest("2"), 2},
		{"a disk from before the annotation", unannotatedCRD, crdManifest("1"), 1},
		{"a disk whose revision does not parse", crdManifest("two"), crdManifest("1"), 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			src := fakeSeedSource(t)
			root := t.TempDir()
			writeManifest(t, filepath.Join(src, likenManifestsRel), "machines-crd.yaml", test.image)
			writeManifest(t, filepath.Join(root, likenManifestsRel), "machines-crd.yaml", test.onDisk)

			seedOrFail(t, root)

			if revision := manifestRevision(t, root, "machines-crd.yaml"); revision != test.revision {
				t.Errorf("the disk keeps revision %d, want the image's %d", revision, test.revision)
			}
		})
	}
}

func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading the file info: %v", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("reading the inode of %s: no inode in the file info", path)
	}
	return stat.Ino
}

// A kept CRD keeps its inode, so the seed neither removed nor rewrote
// it, and a power cut during the seed cannot leave the disk without the
// newer schema.
func TestSeedClusterStateNeitherRemovesNorRewritesAKeptCRD(t *testing.T) {
	src := fakeSeedSource(t)
	root := t.TempDir()
	writeManifest(t, filepath.Join(src, likenManifestsRel), "machines-crd.yaml", crdManifest("1"))
	path := writeManifest(t, filepath.Join(root, likenManifestsRel), "machines-crd.yaml", crdManifest("2"))
	before := inodeOf(t, path)

	seedOrFail(t, root)

	if after := inodeOf(t, path); after != before {
		t.Errorf("the kept manifest is inode %d, want %d, so it was removed and written again", after, before)
	}
}

// Keeping a CRD does not change the rest of the seed: it still replaces
// a workload manifest that exists on the disk, and removes a file that
// the image no longer has.
func TestSeedClusterStateSeedsEverythingElseBesideAKeptCRD(t *testing.T) {
	src := fakeSeedSource(t)
	root := t.TempDir()
	writeManifest(t, filepath.Join(src, likenManifestsRel), "machines-crd.yaml", crdManifest("1"))
	writeManifest(t, filepath.Join(root, likenManifestsRel), "machines-crd.yaml", crdManifest("2"))
	writeManifest(t, filepath.Join(src, likenManifestsRel), "machine-operator.yaml", []byte("kind: DaemonSet\nmetadata:\n  name: new\n"))
	writeManifest(t, filepath.Join(root, likenManifestsRel), "machine-operator.yaml", []byte("kind: DaemonSet\nmetadata:\n  name: old\n"))
	writeManifest(t, filepath.Join(root, likenManifestsRel), "retired.yaml", []byte("kind: DaemonSet\n"))

	seedOrFail(t, root)

	raw, err := os.ReadFile(filepath.Join(root, likenManifestsRel, "machine-operator.yaml"))
	if err != nil {
		t.Fatalf("reading the seeded manifest: %v", err)
	}
	if string(raw) != "kind: DaemonSet\nmetadata:\n  name: new\n" {
		t.Errorf("machine-operator.yaml is %q, want the image's copy", raw)
	}
	if _, err := os.Stat(filepath.Join(root, likenManifestsRel, "seeded")); err != nil {
		t.Errorf("the seeded file is missing: %v, want it copied", err)
	}
	if _, err := os.Stat(filepath.Join(root, likenManifestsRel, "retired.yaml")); !os.IsNotExist(err) {
		t.Errorf("retired.yaml is still on disk (%v), want it deleted because the image does not have it", err)
	}
}

// A manifest that does not parse must not stop a boot, and the seed
// replaces it with the image's copy.
func TestSeedClusterStateSeedsNormallyWhenAManifestDoesNotParse(t *testing.T) {
	src := fakeSeedSource(t)
	root := t.TempDir()
	writeManifest(t, filepath.Join(src, likenManifestsRel), "machines-crd.yaml", crdManifest("1"))
	writeManifest(t, filepath.Join(root, likenManifestsRel), "machines-crd.yaml", []byte("kind: [\n  unterminated\n"))

	seedOrFail(t, root)

	if revision := manifestRevision(t, root, "machines-crd.yaml"); revision != 1 {
		t.Errorf("the disk keeps revision %d, want the image's 1", revision)
	}
}

// init is PID 1, so a read that blocks stops the boot, and a read with
// no size limit can use all of the machine's memory. The guard keeps no
// manifest when either copy is not a regular file of at most
// manifestSizeLimit bytes.
func TestNewerCRDsOnDiskReadsOnlyOrdinaryManifests(t *testing.T) {
	cases := []struct {
		name string
		put  func(t *testing.T, path string)
	}{
		{"a fifo", func(t *testing.T, path string) {
			if err := syscall.Mkfifo(path, 0o644); err != nil {
				t.Fatalf("creating the fifo: %v", err)
			}
		}},
		{"a symlink to a device", func(t *testing.T, path string) {
			if err := os.Symlink("/dev/zero", path); err != nil {
				t.Fatalf("creating the symlink: %v", err)
			}
		}},
		{"a file past the size of any manifest", func(t *testing.T, path string) {
			if err := os.WriteFile(path, make([]byte, manifestSizeLimit+1), 0o644); err != nil {
				t.Fatalf("writing the oversized manifest: %v", err)
			}
		}},
	}
	for _, test := range cases {
		t.Run(test.name+" on the disk", func(t *testing.T) {
			src, dst := t.TempDir(), t.TempDir()
			writeManifest(t, src, "machines-crd.yaml", crdManifest("1"))
			test.put(t, filepath.Join(dst, "machines-crd.yaml"))

			if keep := newerCRDsOnDisk(src, dst); len(keep) != 0 {
				t.Errorf("the kept manifests are %v, want none because a manifest that cannot be read cannot be compared", keep)
			}
		})
		t.Run(test.name+" in the image", func(t *testing.T) {
			src, dst := t.TempDir(), t.TempDir()
			test.put(t, filepath.Join(src, "machines-crd.yaml"))
			writeManifest(t, dst, "machines-crd.yaml", crdManifest("2"))

			if keep := newerCRDsOnDisk(src, dst); len(keep) != 0 {
				t.Errorf("the kept manifests are %v, want none because a manifest that cannot be read cannot be compared", keep)
			}
		})
	}
}

// sealedDir creates a read-only directory with one file in it, so a
// seed into it can neither remove nor create a file.
func sealedDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "stuck.yaml"), []byte("kind: DaemonSet\n"), 0o644); err != nil {
		t.Fatalf("writing the file in the directory: %v", err)
	}
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

// A content seed that fails must not stop the boot. The boot continues
// with the manifests already on the disk, and the seeds after it still
// run.
func TestSeedClusterStateContinuesWhenAContentSeedFails(t *testing.T) {
	fakeSeedSource(t)
	root := t.TempDir()
	sealedDir(t, filepath.Join(root, likenManifestsRel))

	if err := seedClusterState(root); err != nil {
		t.Errorf("seedClusterState returned %v, want nil because a manifest that cannot be copied does not stop the boot", err)
	}

	if _, err := os.Stat(filepath.Join(root, "k3s/agent/images", "seeded")); err != nil {
		t.Errorf("the image seed is missing after the earlier failure: %v, want it copied", err)
	}
	if _, err := os.Stat(filepath.Join(root, likenManifestsRel, "stuck.yaml")); err != nil {
		t.Errorf("stuck.yaml is missing: %v, want the copy on disk kept", err)
	}
}

// stuckEntry creates a directory in dir that RemoveAll cannot remove,
// like an immutable file on a real disk.
func stuckEntry(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("creating the directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "held"), []byte("held\n"), 0o644); err != nil {
		t.Fatalf("writing the file in the directory: %v", err)
	}
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

// An entry that cannot be removed must not affect the other entries.
// If the refresh stopped at the first failure, the entries before it
// would already be removed and never copied back, and a machine that
// failed this way on every boot would run with manifests missing.
func TestRefreshSeedDirIsBestEffortPerEntry(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeManifest(t, src, "liken-system.yaml", []byte("kind: Namespace\nmetadata:\n  name: new\n"))
	writeManifest(t, src, "machines-crd.yaml", crdManifest("2"))
	writeManifest(t, dst, "liken-system.yaml", []byte("kind: Namespace\nmetadata:\n  name: old\n"))
	writeManifest(t, dst, "retired.yaml", []byte("kind: DaemonSet\n"))
	stuckEntry(t, dst, "machines-crd.yaml")

	err := refreshSeedDir(dst, src, map[string]bool{})

	if err == nil {
		t.Fatal("refreshSeedDir returned no error when an entry could not be replaced, want one")
	}
	if !strings.Contains(err.Error(), "machines-crd.yaml") {
		t.Errorf("the error is %v, want it to name machines-crd.yaml, the entry that could not be replaced", err)
	}
	raw, readErr := os.ReadFile(filepath.Join(dst, "liken-system.yaml"))
	if readErr != nil {
		t.Fatalf("reading liken-system.yaml: %v, want the entries beside the stuck one refreshed", readErr)
	}
	if string(raw) != "kind: Namespace\nmetadata:\n  name: new\n" {
		t.Errorf("liken-system.yaml is %q, want the image's copy, because the entries beside the stuck one still refresh", raw)
	}
	if _, err := os.Stat(filepath.Join(dst, "retired.yaml")); !os.IsNotExist(err) {
		t.Errorf("retired.yaml is still on disk (%v), want it deleted because the image does not have it", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "machines-crd.yaml", "held")); err != nil {
		t.Errorf("machines-crd.yaml/held is missing: %v, want the entry that could not be replaced left as it was", err)
	}
}

// A failure of the identity seed stops the boot, unlike a content seed.
// Without the cluster's CAs on the disk, k3s generates its own, and the
// machine serves an API that no kubeconfig in this image can verify
// while it still reports itself healthy.
func TestSeedClusterStateStopsWhenTheIdentitySeedFails(t *testing.T) {
	fakeSeedSource(t)
	root := t.TempDir()
	sealed := filepath.Join(root, "k3s")
	if err := os.MkdirAll(sealed, 0o555); err != nil {
		t.Fatalf("creating the read-only directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

	if err := seedClusterState(root); err == nil {
		t.Error("seedClusterState returned no error, want one when the machine cannot take the cluster's identity")
	}
}

// seededManifests returns the path of every manifest file that
// image/build.sh copies into liken's subdirectory of k3s's auto-deploy
// directory. It reads the list from the script, so a new seeded
// manifest is covered without a change here.
func seededManifests(t *testing.T) []string {
	t.Helper()
	script, err := os.ReadFile(buildScript)
	if err != nil {
		t.Fatalf("reading the build script: %v", err)
	}
	// Read only the loop that writes liken's subdirectory. The feature
	// manifests later in the script go to /etc/liken/features and are
	// not seeded.
	block := string(script)
	start := strings.Index(block, `mkdir -p "$root/var/lib/rancher/`+likenManifestsRel+`"`)
	if start < 0 {
		t.Fatalf("%s does not name the seeded manifests in the form this test reads", buildScript)
	}
	block = block[start:]
	if end := strings.Index(block, "\ndone\n"); end >= 0 {
		block = block[:end]
	}
	seeded := regexp.MustCompile(`"\$here"/\.\./([\w-]+)/manifests/\*\.yaml`)
	domains := seeded.FindAllStringSubmatch(block, -1)
	if len(domains) == 0 {
		t.Fatalf("%s does not name the seeded manifests in the form this test reads", buildScript)
	}
	var paths []string
	for _, domain := range domains {
		matched, err := filepath.Glob(filepath.Join("..", domain[1], "manifests", "*.yaml"))
		if err != nil {
			t.Fatalf("listing the manifests of a component: %v", err)
		}
		paths = append(paths, matched...)
	}
	return paths
}

func crdDocumentCount(t *testing.T, raw []byte) int {
	t.Helper()
	count := 0
	for _, doc := range yamlDocuments(raw) {
		body, err := yaml.YAMLToJSON(doc)
		if err != nil {
			t.Fatalf("converting a manifest document to JSON: %v", err)
		}
		var meta struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(body, &meta); err != nil {
			t.Fatalf("decoding a manifest document: %v", err)
		}
		if meta.Kind == "CustomResourceDefinition" {
			count++
		}
	}
	return count
}

// The guard reads one revision per file, and schemaPins records one
// fingerprint per file, so a second CRD in a file would have no
// revision of its own.
func TestASeededManifestHoldsAtMostOneCRD(t *testing.T) {
	for _, path := range seededManifests(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the manifest: %v", err)
			}
			if count := crdDocumentCount(t, raw); count > 1 {
				t.Errorf("%s declares %d CustomResourceDefinitions, want at most 1, because the guard reads one revision per file",
					path, count)
			}
		})
	}
}

// schemaChildren lists the keys of an OpenAPI schema whose values are
// schemas, and how each one nests them: "map" is a map from names to
// schemas, "schema" is one schema, and "list" is a list of schemas.
// The other keys of a schema node hold values, such as a default, an
// enum, or a validation message. A `description` inside such a value
// is part of the value, not documentation.
var schemaChildren = map[string]string{
	"properties":           "map",
	"patternProperties":    "map",
	"definitions":          "map",
	"items":                "schema",
	"additionalProperties": "schema",
	"not":                  "schema",
	"allOf":                "list",
	"anyOf":                "list",
	"oneOf":                "list",
}

// withoutDescriptions removes the descriptions from a schema, so the
// fingerprint changes with the shape of the API and not with its
// documentation.
func withoutDescriptions(node any) any {
	schema, ok := node.(map[string]any)
	if !ok {
		return node
	}
	stripped := map[string]any{}
	for key, child := range schema {
		_, isText := child.(string)
		if key == "description" && isText {
			continue
		}
		stripped[key] = child
		switch schemaChildren[key] {
		case "map":
			if named, ok := child.(map[string]any); ok {
				each := map[string]any{}
				for name, value := range named {
					each[name] = withoutDescriptions(value)
				}
				stripped[key] = each
			}
		case "schema":
			stripped[key] = withoutDescriptions(child)
		case "list":
			if items, ok := child.([]any); ok {
				each := make([]any, len(items))
				for index, value := range items {
					each[index] = withoutDescriptions(value)
				}
				stripped[key] = each
			}
		}
	}
	return stripped
}

// schemaFingerprint returns the SHA-256 of a CRD manifest's spec with
// the schema descriptions removed. encoding/json writes the keys of a
// map in sorted order, so the hash depends on the content and not on
// the order of the keys in the file.
func schemaFingerprint(t *testing.T, raw []byte) string {
	t.Helper()
	body, err := yaml.YAMLToJSON(raw)
	if err != nil {
		t.Fatalf("converting the manifest to JSON: %v", err)
	}
	var crd map[string]any
	if err := json.Unmarshal(body, &crd); err != nil {
		t.Fatalf("decoding the manifest: %v", err)
	}
	spec, ok := crd["spec"].(map[string]any)
	if !ok {
		t.Fatal("the manifest has no spec, want a CustomResourceDefinition spec")
	}
	versions, _ := spec["versions"].([]any)
	for _, version := range versions {
		declared, ok := version.(map[string]any)["schema"].(map[string]any)
		if !ok {
			continue
		}
		declared["openAPIV3Schema"] = withoutDescriptions(declared["openAPIV3Schema"])
	}
	shape, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("encoding the schema: %v", err)
	}
	sum := sha256.Sum256(shape)
	return hex.EncodeToString(sum[:])
}

// fingerprintCRD returns a CRD manifest with one property. The
// property's description is documentation. The `description` inside its
// default is a field of the default value that the API server stores.
func fingerprintCRD(documentation, defaulted string) []byte {
	return []byte(`apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: machines.liken.sh
spec:
  group: liken.sh
  versions:
    - name: v1alpha1
      schema:
        openAPIV3Schema:
          type: object
          properties:
            spec:
              type: object
              description: ` + documentation + `
              default:
                description: ` + defaulted + `
`)
}

func TestTheFingerprintIgnoresDocumentation(t *testing.T) {
	first := schemaFingerprint(t, fingerprintCRD("what the field is for", "a default"))
	second := schemaFingerprint(t, fingerprintCRD("the same field, said again", "a default"))
	if first != second {
		t.Errorf("the fingerprints are %s and %s, want the same when only a description changes", first, second)
	}
}

func TestTheFingerprintSeesADefaultValueChange(t *testing.T) {
	first := schemaFingerprint(t, fingerprintCRD("what the field is for", "a default"))
	second := schemaFingerprint(t, fingerprintCRD("what the field is for", "another default"))
	if first == second {
		t.Errorf("the fingerprint is %s for both, want a different one when a default value changes", first)
	}
}

// schemaPins records the revision and the fingerprint of each seeded
// CRD manifest.
var schemaPins = map[string]struct {
	revision    int
	fingerprint string
}{
	"machines-crd.yaml":                 {16, "d9f0aba335cf12d34366f118e9e75f7ca6c70bcfba86f9ec61ee5d3049b0ae92"},
	"clusters-crd.yaml":                 {6, "e48401f10d56d569f2871ee2180a3734f7b66f8ba2225a24b2e17b7de24cd283"},
	"disruptions-crd.yaml":              {4, "a5539e639ddda2982951c5689ba937217f1c7f40bf52813a059fd68d58b6ec21"},
	"machinedisruptionbudgets-crd.yaml": {2, "7daf916506a8b287eaeb73c1526935c33a2f60084dbebc5b32ca04c831462ac8"},
}

// The guard keeps the disk's CRD only when its revision is higher, so a
// schema that changes without a higher revision is not protected. This
// test fails when a seeded CRD's fingerprint or revision differs from
// schemaPins, so every schema change must raise the revision.
func TestTheSeededCRDsPinTheirSchemaRevision(t *testing.T) {
	pinned := map[string]bool{}
	for _, path := range seededManifests(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("reading the manifest: %v", err)
			}
			declared, err := crdSchemaRevision(raw)
			if err != nil {
				t.Fatalf("reading the schema revision: %v", err)
			}
			if !declared.isCRD {
				return
			}
			name := filepath.Base(path)
			pinned[name] = true
			pin, recorded := schemaPins[name]
			if !recorded {
				t.Fatalf("%s ships a CustomResourceDefinition that no pin covers. Give it %s, "+
					"and record it in schemaPins as {%d, %q}.",
					path, schemaRevisionAnnotation, declared.revision, schemaFingerprint(t, raw))
			}
			if declared.revision != pin.revision {
				t.Errorf("%s declares %s: %d, and schemaPins records %d, so change both together",
					path, schemaRevisionAnnotation, declared.revision, pin.revision)
			}
			if got := schemaFingerprint(t, raw); got != pin.fingerprint {
				t.Errorf("the schema in %s changed. Raise %s past %d in the manifest, and record "+
					"the new fingerprint %s in schemaPins. A schema that changes without a higher "+
					"revision lets a slot with an older release write its older schema over it.",
					path, schemaRevisionAnnotation, declared.revision, got)
			}
		})
	}
	for name := range schemaPins {
		if !pinned[name] {
			t.Errorf("schemaPins records %s, and the seed no longer ships it as a CRD", name)
		}
	}
}
