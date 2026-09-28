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
		t.Fatal(err)
	}
	b, hashB, err := RenderImportedImages(map[string]string{
		"liken-logs.tar":             "bbb",
		"liken-machine-operator.tar": "aaa",
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) || hashA != hashB {
		t.Fatalf("the same images rendered differently:\n%s\n%s", a, b)
	}
	if !strings.Contains(string(a), "kind: ImportedImages") {
		t.Fatalf("rendering lacks the kind: %s", a)
	}
}

func TestRenderImportedImagesNamesEveryTarball(t *testing.T) {
	raw, _, err := RenderImportedImages(map[string]string{"liken-logs.tar": "abc123"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "liken-logs.tar: abc123") {
		t.Fatalf("the rendering does not name the tarball's digest:\n%s", raw)
	}
}

func TestRenderImportedImagesWithNoTarballs(t *testing.T) {
	raw, hash, err := RenderImportedImages(nil)
	if err != nil {
		t.Fatal(err)
	}
	if hash == "" {
		t.Fatal("an empty record still has an identity")
	}
	if strings.Contains(string(raw), "images:") {
		t.Fatalf("an empty record should omit its images map:\n%s", raw)
	}
}

func TestImportedImagesStoreHasItsOwnDirectory(t *testing.T) {
	root := t.TempDir()
	store := ImportedImagesStore(root)
	if err := store.WriteStaged([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "imports", "staged.yaml")); err != nil {
		t.Fatalf("the store did not write under imports/: %v", err)
	}
}

func TestHashImageTarballs(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "liken-logs.tar"), []byte("logs"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "liken-machine-operator.tar"), []byte("operator"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("not a tarball"), 0o644); err != nil {
		t.Fatal(err)
	}

	digests, err := HashImageTarballs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(digests) != 2 {
		t.Fatalf("expected the two tarballs and nothing else, got %v", digests)
	}
	if digests["liken-logs.tar"] == digests["liken-machine-operator.tar"] {
		t.Fatal("different tarballs hashed identically")
	}
}

func TestHashImageTarballsSeesContentChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "liken-logs.tar")
	if err := os.WriteFile(path, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := HashImageTarballs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := HashImageTarballs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if before["liken-logs.tar"] == after["liken-logs.tar"] {
		t.Fatal("a content change did not change the digest")
	}
}

func TestHashImageTarballsWithoutADirectory(t *testing.T) {
	digests, err := HashImageTarballs(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatal(err)
	}
	if digests != nil {
		t.Fatalf("a missing directory means no tarballs, got %v", digests)
	}
}

func TestHashImageTarballsReportsAnUnreadableTarball(t *testing.T) {
	dir := t.TempDir()
	unreadableFile(t, filepath.Join(dir, "liken-logs.tar"))
	if _, err := HashImageTarballs(dir); err == nil {
		t.Error("a tarball that exists but can't be read is an error, not an absent tarball")
	}
}

func TestHashImageTarballsReportsAnUnlistableDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, []byte("plain file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := HashImageTarballs(path); err == nil {
		t.Error("a path that exists but isn't a directory is an error, not an empty one")
	}
}
