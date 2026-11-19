package machine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderImportedImagesIsCanonical(t *testing.T) {
	a, hashA, err := RenderImportedImages(map[string]string{
		"liken-machine-operator.tar": "aaa",
		"liken-logs.tar":             "bbb",
	})
	if err != nil {
		t.Fatalf("rendering the first set of images: %v", err)
	}
	b, hashB, err := RenderImportedImages(map[string]string{
		"liken-logs.tar":             "bbb",
		"liken-machine-operator.tar": "aaa",
	})
	if err != nil {
		t.Fatalf("rendering the same images in another order: %v", err)
	}
	if string(a) != string(b) || hashA != hashB {
		t.Fatalf("the same images rendered to different bytes, want identical bytes:\n%s\n%s", a, b)
	}
	if !strings.Contains(string(a), "kind: ImportedImages") {
		t.Fatalf("the rendering has no kind: ImportedImages line, want one:\n%s", a)
	}
}

func TestRenderImportedImagesNamesEveryTarball(t *testing.T) {
	raw, _, err := RenderImportedImages(map[string]string{"liken-logs.tar": "abc123"})
	if err != nil {
		t.Fatalf("rendering one tarball: %v", err)
	}
	if !strings.Contains(string(raw), "liken-logs.tar: abc123") {
		t.Fatalf("the rendering has no liken-logs.tar: abc123 entry, want one:\n%s", raw)
	}
}

func TestRenderImportedImagesWithNoTarballs(t *testing.T) {
	raw, hash, err := RenderImportedImages(nil)
	if err != nil {
		t.Fatalf("rendering no tarballs: %v", err)
	}
	if hash == "" {
		t.Fatal("the hash of an empty record is empty, want a hash")
	}
	if strings.Contains(string(raw), "images:") {
		t.Fatalf("an empty record has an images map, want none:\n%s", raw)
	}
}

func TestImportedImagesStoreHasItsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	store := ImportedImagesStore(root)
	if err := store.WriteStaged([]byte("x")); err != nil {
		t.Fatalf("writing the staged record: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "imports", "staged.yaml")); err != nil {
		t.Fatalf("stat of imports/staged.yaml: %v, want the store to write it", err)
	}
}

func TestHashImageTarballs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "liken-logs.tar"), []byte("logs"), 0o644); err != nil {
		t.Fatalf("writing liken-logs.tar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "liken-machine-operator.tar"), []byte("operator"), 0o644); err != nil {
		t.Fatalf("writing liken-machine-operator.tar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("not a tarball"), 0o644); err != nil {
		t.Fatalf("writing README.txt: %v", err)
	}

	digests, err := HashImageTarballs(dir)
	if err != nil {
		t.Fatalf("hashing the tarballs: %v", err)
	}
	if len(digests) != 2 {
		t.Fatalf("digests are %v, want only liken-logs.tar and liken-machine-operator.tar", digests)
	}
	if digests["liken-logs.tar"] == digests["liken-machine-operator.tar"] {
		t.Fatal("different tarballs have the same digest, want different digests")
	}
}

func TestHashImageTarballsChangesDigestWhenContentChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "liken-logs.tar")
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatalf("writing the first tarball: %v", err)
	}
	before, err := HashImageTarballs(dir)
	if err != nil {
		t.Fatalf("hashing the first tarball: %v", err)
	}
	if err := os.WriteFile(path, []byte("v2"), 0o644); err != nil {
		t.Fatalf("writing the changed tarball: %v", err)
	}
	after, err := HashImageTarballs(dir)
	if err != nil {
		t.Fatalf("hashing the changed tarball: %v", err)
	}
	if before["liken-logs.tar"] == after["liken-logs.tar"] {
		t.Fatal("the digest is the same after the content changed, want a different digest")
	}
}

func TestHashImageTarballsWithoutADirectory(t *testing.T) {
	digests, err := HashImageTarballs(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("hashing a missing directory: %v", err)
	}
	if digests != nil {
		t.Fatalf("digests of a missing directory are %v, want none", digests)
	}
}

func TestHashImageTarballsReportsAnUnreadableTarball(t *testing.T) {
	dir := t.TempDir()
	unreadableFile(t, filepath.Join(dir, "liken-logs.tar"))
	if _, err := HashImageTarballs(dir); err == nil {
		t.Error("hashing a tarball that cannot be read returned no error, want one")
	}
}

func TestHashImageTarballsReportsAnUnlistableDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("plain file"), 0o644); err != nil {
		t.Fatalf("writing the plain file: %v", err)
	}
	if _, err := HashImageTarballs(path); err == nil {
		t.Error("hashing a path that is not a directory returned no error, want one")
	}
}
