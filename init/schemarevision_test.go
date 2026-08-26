package main

// These tests cover the three parts of the schema-downgrade guard:
// reading the schema-revision annotation out of a manifest, the seed
// that leaves a newer CRD on the disk untouched, and the pin that
// ties every shipped schema to its recorded revision so a schema
// change cannot ship without raising it.

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

// crdManifest renders a CustomResourceDefinition that declares the
// given schema revision.
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

// unannotatedCRD is the manifest an older release ships: a CRD with
// no schema revision on it.
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
			schemaRevision{isCRD: true, bad: "two"}, false},
		{"a negative revision", crdManifest("-3"), schemaRevision{isCRD: true, bad: "-3"}, false},
		{"a negative revision past the width of an int", crdManifest("-99999999999999999999"),
			schemaRevision{isCRD: true, bad: "-99999999999999999999"}, false},
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
				t.Fatalf("error %v, wanted a failure: %v", err, test.fails)
			}
			if got != test.want {
				t.Errorf("read %+v, wanted %+v", got, test.want)
			}
		})
	}
}

// writeManifest puts one manifest in a manifests directory, making
// the directory when it is not there yet.
func writeManifest(t *testing.T, dir, name string, raw []byte) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// manifestRevision reads back the schema revision of the manifest
// that the seed left on the disk.
func manifestRevision(t *testing.T, root, name string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, likenManifestsRel, name))
	if err != nil {
		t.Fatal(err)
	}
	got, err := crdSchemaRevision(raw)
	if err != nil {
		t.Fatal(err)
	}
	return got.revision
}

// seedOrFail seeds and stops the test when the identity seed cannot
// land, which no test here arranges.
func seedOrFail(t *testing.T, root string) {
	t.Helper()
	if err := seedClusterState(root); err != nil {
		t.Fatal(err)
	}
}

// A machine that falls back to the older slot boots an image whose
// CRD manifest declares an older schema. Replanting it downgrades the
// CRD in the API server, and every status write then drops the fields
// the older schema does not know.
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
				t.Errorf("the disk keeps revision %d, wanted the newer %d", revision, test.revision)
			}
		})
	}
}

func TestSeedClusterStateTakesTheImagesCRDWhenTheDiskIsNotNewer(t *testing.T) {
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
				t.Errorf("the disk keeps revision %d, wanted the image's %d", revision, test.revision)
			}
		})
	}
}

// inodeOf identifies a file on the disk, so that a test can tell a
// file that stayed from a file that was removed and written again.
func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("no inode for %s", path)
	}
	return stat.Ino
}

// The kept CRD is never removed and never rewritten: the seed passes
// it over on both halves of the refresh. So there is no moment when
// the disk holds no copy of the schema the cluster serves, and no
// write that a power cut can tear.
func TestSeedClusterStateNeverLiftsTheKeptCRDOffTheDisk(t *testing.T) {
	src := fakeSeedSource(t)
	root := t.TempDir()
	writeManifest(t, filepath.Join(src, likenManifestsRel), "machines-crd.yaml", crdManifest("1"))
	path := writeManifest(t, filepath.Join(root, likenManifestsRel), "machines-crd.yaml", crdManifest("2"))
	before := inodeOf(t, path)

	seedOrFail(t, root)

	if after := inodeOf(t, path); after != before {
		t.Errorf("the kept manifest is inode %d, was %d; it left the disk and came back", after, before)
	}
}

// The guard holds back one file. Everything else the image carries
// still lands, including a workload manifest that the disk also has,
// and a file the disk holds that the image has dropped goes away.
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
		t.Fatal(err)
	}
	if string(raw) != "kind: DaemonSet\nmetadata:\n  name: new\n" {
		t.Errorf("the image's workload manifest must land: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(root, likenManifestsRel, "seeded")); err != nil {
		t.Errorf("the rest of the seed still lands: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, likenManifestsRel, "retired.yaml")); !os.IsNotExist(err) {
		t.Errorf("a manifest the image no longer carries must go: %v", err)
	}
}

// A manifest that does not parse must never stop a boot. The guard
// gives up on that file and the seed refreshes it as it always did.
func TestSeedClusterStateSeedsNormallyWhenAManifestDoesNotParse(t *testing.T) {
	src := fakeSeedSource(t)
	root := t.TempDir()
	writeManifest(t, filepath.Join(src, likenManifestsRel), "machines-crd.yaml", crdManifest("1"))
	writeManifest(t, filepath.Join(root, likenManifestsRel), "machines-crd.yaml", []byte("kind: [\n  unterminated\n"))

	seedOrFail(t, root)

	if revision := manifestRevision(t, root, "machines-crd.yaml"); revision != 1 {
		t.Errorf("the disk keeps revision %d, wanted the image's 1", revision)
	}
}

// init is PID 1. A read that blocks is a machine that never boots,
// and a read that is unbounded is a machine that runs out of memory,
// so the guard reads a candidate only when it is an ordinary file of
// an ordinary size. It compares nothing it could not read, which
// leaves the image's copy to land as it always did.
func TestNewerCRDsOnDiskReadsOnlyOrdinaryManifests(t *testing.T) {
	cases := []struct {
		name string
		put  func(t *testing.T, path string)
	}{
		{"a fifo", func(t *testing.T, path string) {
			if err := syscall.Mkfifo(path, 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"a symlink to a device", func(t *testing.T, path string) {
			if err := os.Symlink("/dev/zero", path); err != nil {
				t.Fatal(err)
			}
		}},
		{"a file past the size of any manifest", func(t *testing.T, path string) {
			if err := os.WriteFile(path, make([]byte, manifestSizeLimit+1), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range cases {
		t.Run(test.name+" on the disk", func(t *testing.T) {
			src, dst := t.TempDir(), t.TempDir()
			writeManifest(t, src, "machines-crd.yaml", crdManifest("1"))
			test.put(t, filepath.Join(dst, "machines-crd.yaml"))

			if keep := newerCRDsOnDisk(src, dst); len(keep) != 0 {
				t.Errorf("kept %v; a manifest it cannot read is a manifest it cannot compare", keep)
			}
		})
		t.Run(test.name+" in the image", func(t *testing.T) {
			src, dst := t.TempDir(), t.TempDir()
			test.put(t, filepath.Join(src, "machines-crd.yaml"))
			writeManifest(t, dst, "machines-crd.yaml", crdManifest("2"))

			if keep := newerCRDsOnDisk(src, dst); len(keep) != 0 {
				t.Errorf("kept %v; a manifest it cannot read is a manifest it cannot compare", keep)
			}
		})
	}
}

// sealedDir makes a directory that nothing can be removed from or
// written into, so that a seed of it fails.
func sealedDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "stuck.yaml"), []byte("kind: DaemonSet\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

// A content seed that cannot be written is not worth a machine. The
// boot goes on with the manifests the disk already holds, which the
// cluster can report, and the seeds that do work still land.
func TestSeedClusterStateGoesOnWhenAContentSeedFails(t *testing.T) {
	fakeSeedSource(t)
	root := t.TempDir()
	sealedDir(t, filepath.Join(root, likenManifestsRel))

	if err := seedClusterState(root); err != nil {
		t.Errorf("a manifest seed is not worth a machine: %v", err)
	}

	if _, err := os.Stat(filepath.Join(root, "k3s/agent/images", "seeded")); err != nil {
		t.Errorf("the seeds after the failure still land: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, likenManifestsRel, "stuck.yaml")); err != nil {
		t.Errorf("the boot keeps what the disk holds: %v", err)
	}
}

// stuckEntry puts an entry in a directory that RemoveAll cannot take
// away, the way an immutable file refuses unlinkat, while everything
// beside it stays removable.
func stuckEntry(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "held"), []byte("held\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

// One entry that cannot be removed costs exactly itself. A drill
// against an immutable machines-crd.yaml left the seed directory
// holding that one file: the entries before it in directory order
// were already gone, and the copy never ran. A machine that failed
// this way on every boot would run with most of its manifests missing
// from the disk.
func TestRefreshSeedDirIsBestEffortPerEntry(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeManifest(t, src, "liken-system.yaml", []byte("kind: Namespace\nmetadata:\n  name: new\n"))
	writeManifest(t, src, "machines-crd.yaml", crdManifest("2"))
	writeManifest(t, dst, "liken-system.yaml", []byte("kind: Namespace\nmetadata:\n  name: old\n"))
	writeManifest(t, dst, "retired.yaml", []byte("kind: DaemonSet\n"))
	stuckEntry(t, dst, "machines-crd.yaml")

	err := refreshSeedDir(dst, src, map[string]bool{})

	if err == nil {
		t.Fatal("a refresh that could not reach every entry must say so")
	}
	if !strings.Contains(err.Error(), "machines-crd.yaml") {
		t.Errorf("the error must name the entry that stuck: %v", err)
	}
	raw, readErr := os.ReadFile(filepath.Join(dst, "liken-system.yaml"))
	if readErr != nil {
		t.Fatalf("the entries beside it still refresh: %v", readErr)
	}
	if string(raw) != "kind: Namespace\nmetadata:\n  name: new\n" {
		t.Errorf("the entries beside it still refresh: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(dst, "retired.yaml")); !os.IsNotExist(err) {
		t.Errorf("an entry the image no longer carries still goes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "machines-crd.yaml", "held")); err != nil {
		t.Errorf("the entry that stuck stays as it was: %v", err)
	}
}

// The identity seed is the other half of the policy. A machine that
// cannot take the cluster's CAs onto its disk would mint its own, and
// then serve an API that no kubeconfig this image carries can verify
// and no machine holding this image's token can join. It reports
// itself healthy while it does that, so this failure stops the boot
// and is recorded for the next one.
func TestSeedClusterStateStopsWhenTheIdentitySeedFails(t *testing.T) {
	fakeSeedSource(t)
	root := t.TempDir()
	sealed := filepath.Join(root, "k3s")
	if err := os.MkdirAll(sealed, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sealed, 0o755) })

	if err := seedClusterState(root); err == nil {
		t.Error("a machine that cannot take the cluster's identity must not come up")
	}
}

// seededManifests names every manifest file that the image bakes into
// k3s's auto-deploy directory. The list comes off image/build.sh, the
// one place that decides what is seeded, so a domain that starts
// shipping a manifest is covered here without anybody adding it.
func seededManifests(t *testing.T) []string {
	t.Helper()
	script, err := os.ReadFile(buildScript)
	if err != nil {
		t.Fatal(err)
	}
	// The one loop that writes into liken's subdirectory of k3s's
	// auto-deploy directory. The feature manifests further down the
	// script go to /etc/liken/features and are not seeded.
	block := string(script)
	start := strings.Index(block, `mkdir -p "$root/var/lib/rancher/`+likenManifestsRel+`"`)
	if start < 0 {
		t.Fatalf("%s no longer names the seeded manifests the way this test reads them", buildScript)
	}
	block = block[start:]
	if end := strings.Index(block, "\ndone\n"); end >= 0 {
		block = block[:end]
	}
	seeded := regexp.MustCompile(`"\$here"/\.\./([\w-]+)/manifests/\*\.yaml`)
	domains := seeded.FindAllStringSubmatch(block, -1)
	if len(domains) == 0 {
		t.Fatalf("%s no longer names the seeded manifests the way this test reads them", buildScript)
	}
	var paths []string
	for _, domain := range domains {
		matched, err := filepath.Glob(filepath.Join("..", domain[1], "manifests", "*.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, matched...)
	}
	return paths
}

// crdDocumentCount counts the CustomResourceDefinitions in one file.
func crdDocumentCount(t *testing.T, raw []byte) int {
	t.Helper()
	count := 0
	for _, doc := range yamlDocuments(raw) {
		body, err := yaml.YAMLToJSON(doc)
		if err != nil {
			t.Fatal(err)
		}
		var meta struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(body, &meta); err != nil {
			t.Fatal(err)
		}
		if meta.Kind == "CustomResourceDefinition" {
			count++
		}
	}
	return count
}

// The guard reads one revision for one file, and the pin below
// records one hash for one file. A file that carried two CRDs would
// give both of them one revision between them, so the seed holds to
// one CRD per file.
func TestASeededManifestHoldsAtMostOneCRD(t *testing.T) {
	for _, path := range seededManifests(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if count := crdDocumentCount(t, raw); count > 1 {
				t.Errorf("%s declares %d CustomResourceDefinitions; the guard reads one revision per file",
					path, count)
			}
		})
	}
}

// schemaChildren names the keys of an OpenAPI schema whose values are
// themselves schemas. Everything else a schema node holds is a value
// (a default, an enum, a validation message), and a `description`
// inside one of those is part of that value, not documentation.
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

// withoutDescriptions drops the documentation from a schema, so that
// the pin tracks the shape of the API and not the words that describe
// it. It walks only through schema nodes, so a description that
// belongs to a default value stays.
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

// schemaFingerprint hashes the shape of a CRD manifest's spec.
// encoding/json sorts a map's keys, so the hash depends on the
// content and not on the order the file writes it in.
func schemaFingerprint(t *testing.T, raw []byte) string {
	t.Helper()
	body, err := yaml.YAMLToJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	var crd map[string]any
	if err := json.Unmarshal(body, &crd); err != nil {
		t.Fatal(err)
	}
	spec, ok := crd["spec"].(map[string]any)
	if !ok {
		t.Fatalf("the manifest declares no spec")
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
		t.Fatal(err)
	}
	sum := sha256.Sum256(shape)
	return hex.EncodeToString(sum[:])
}

// fingerprintCRD renders a one-field CRD whose property carries the
// given documentation and default, for the two tests below.
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
		t.Error("a reworded description is not a schema change")
	}
}

// A `description` under a `default` is a field of the default value
// that the API server stores, not documentation.
func TestTheFingerprintSeesADefaultValueChange(t *testing.T) {
	first := schemaFingerprint(t, fingerprintCRD("what the field is for", "a default"))
	second := schemaFingerprint(t, fingerprintCRD("what the field is for", "another default"))
	if first == second {
		t.Error("a changed default value is a schema change")
	}
}

// schemaPins records what each seeded CRD declares today. Both
// numbers move together, or the test below fails.
var schemaPins = map[string]struct {
	revision    int
	fingerprint string
}{
	"machines-crd.yaml": {2, "a2da7d8ee023705633d35d4889e60cef0a5ca545114efed3a2216f11e45bfb89"},
	"clusters-crd.yaml": {1, "77ee2b082f87649aae5534d385567e10fd62f9b9d19b789c9da2ac453b826c14"},
}

// TestTheSeededCRDsPinTheirSchemaRevision is the enforcement behind
// the guard in schemarevision.go. The guard keeps the newer schema
// only when the newer schema declares a higher revision, so a schema
// that changes without a bump defeats it. This test holds every
// seeded CRD against a recorded hash, and fails the moment one moves.
func TestTheSeededCRDsPinTheirSchemaRevision(t *testing.T) {
	pinned := map[string]bool{}
	for _, path := range seededManifests(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			declared, err := crdSchemaRevision(raw)
			if err != nil {
				t.Fatal(err)
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
				t.Errorf("%s declares %s: %d, and schemaPins records %d; change both together",
					path, schemaRevisionAnnotation, declared.revision, pin.revision)
			}
			if got := schemaFingerprint(t, raw); got != pin.fingerprint {
				t.Errorf("the schema in %s changed. Raise %s past %d in the manifest, and record "+
					"the new fingerprint %s in schemaPins. A schema that changes without a higher "+
					"revision lets an older slot replant it.",
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
