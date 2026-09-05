package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFiles writes every named file under dir, making the directories
// it needs.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("making %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
}

// storeWith is a store in a temporary directory and a repository in it
// for the URL.
func storeWith(t *testing.T, url string) (*store, *repository) {
	t.Helper()
	holder := newStore(t.TempDir())
	return holder, holder.repository(url)
}

// fileURL is the file:// URL of a repository the test made.
func fileURL(dir string) string {
	return "file://" + dir
}

// checkoutOf fetches the ref and checks it out, returning the tree.
func checkoutOf(t *testing.T, repo *repository, ref string) string {
	t.Helper()
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.fetch(t.Context(), nil, ref, 0); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	commit, err := repo.resolve(t.Context(), ref)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	tree := filepath.Join(t.TempDir(), "tree")
	if err := repo.checkout(t.Context(), commit, tree); err != nil {
		t.Fatalf("checkout: %v", err)
	}
	return tree
}

func TestTheStoreNamesARepositoryByTheHashOfItsURL(t *testing.T) {
	holder, repo := storeWith(t, "https://example.com/data.git")
	sum := sha256.Sum256([]byte("https://example.com/data.git"))
	want := filepath.Join(holder.root, "repos", hex.EncodeToString(sum[:]))
	if repo.dir != want {
		t.Errorf("the repository is at %q, want %q", repo.dir, want)
	}
	if other := holder.repository("https://example.com/other.git"); other.dir == repo.dir {
		t.Error("two URLs named the same directory")
	}
}

func TestTheStoreNamesAVolumeByItsID(t *testing.T) {
	holder := newStore(t.TempDir())
	want := filepath.Join(holder.root, "volumes", "csi-abc")
	if got := holder.volumeDir("csi-abc"); got != want {
		t.Errorf("the volume is at %q, want %q", got, want)
	}
}

func TestCreateWritesTheURLBesideTheRepository(t *testing.T) {
	_, repo := storeWith(t, "https://example.com/data.git")
	if repo.exists() {
		t.Fatal("the repository exists before it is created")
	}
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !repo.exists() {
		t.Error("the repository does not exist after it is created")
	}
	url, err := os.ReadFile(filepath.Join(repo.dir, "url"))
	if err != nil {
		t.Fatalf("reading the url file: %v", err)
	}
	if strings.TrimSpace(string(url)) != "https://example.com/data.git" {
		t.Errorf("the url file says %q", url)
	}
	if bare := git(t, repo.dir, "rev-parse", "--is-bare-repository"); strings.TrimSpace(bare) != "true" {
		t.Errorf("the repository is not bare: %q", bare)
	}
}

func TestCreateReportsADirectoryItCannotMake(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
	holder := newStore(file)
	if err := holder.repository("https://example.com/data.git").create(t.Context()); err == nil {
		t.Error("create answered no error under a file")
	}
}

func TestFetchAndCheckoutBringTheRefIntoATree(t *testing.T) {
	source := repositoryWithACommit(t, map[string]string{
		"a.txt":      "one",
		"docs/b.txt": "two",
	})
	_, repo := storeWith(t, fileURL(source))
	tree := checkoutOf(t, repo, "main")

	for name, want := range map[string]string{"a.txt": "one", "docs/b.txt": "two"} {
		got, err := os.ReadFile(filepath.Join(tree, filepath.FromSlash(name)))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("%s holds %q, want %q", name, got, want)
		}
	}
	if _, err := os.Stat(filepath.Join(tree, ".git")); err == nil {
		t.Error("the tree holds a .git")
	}
	if _, err := os.Stat(tree + ".index"); err == nil {
		t.Error("the index outlived the checkout")
	}
}

func TestFetchTakesTheDepthItIsGiven(t *testing.T) {
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	commitFiles(t, source, map[string]string{"a.txt": "two"})
	commitFiles(t, source, map[string]string{"a.txt": "three"})

	_, repo := storeWith(t, fileURL(source))
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.fetch(t.Context(), nil, "main", 1); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	history := git(t, repo.dir, "rev-list", "--count", refPrefix+"main")
	if strings.TrimSpace(history) != "1" {
		t.Errorf("a depth of 1 fetched %s commits, want 1", strings.TrimSpace(history))
	}
}

func TestFetchReportsARefTheRemoteDoesNotHave(t *testing.T) {
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	_, repo := storeWith(t, fileURL(source))
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("create: %v", err)
	}
	err := repo.fetch(t.Context(), nil, "release", 0)
	if err == nil {
		t.Fatal("fetch answered no error for a ref the remote does not have")
	}
	if !strings.Contains(err.Error(), "release") {
		t.Errorf("fetch said %q, want the ref named", err)
	}
}

func TestFetchReportsARemoteThatIsNotThere(t *testing.T) {
	_, repo := storeWith(t, fileURL(filepath.Join(t.TempDir(), "gone")))
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.fetch(t.Context(), nil, "main", 0); err == nil {
		t.Error("fetch answered no error for a remote that is not there")
	}
}

func TestResolveReportsARefTheStoreDoesNotHold(t *testing.T) {
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	_, repo := storeWith(t, fileURL(source))
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.resolve(t.Context(), "main"); err == nil {
		t.Error("resolve answered no error before any fetch")
	}
}

func TestResolveAnswersTheCommitTheRefNames(t *testing.T) {
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	want := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))
	_, repo := storeWith(t, fileURL(source))
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.fetch(t.Context(), nil, "main", 0); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	got, err := repo.resolve(t.Context(), "main")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != want {
		t.Errorf("resolve answered %q, want %q", got, want)
	}
}

func TestCheckoutReportsATreeItCannotMake(t *testing.T) {
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	_, repo := storeWith(t, fileURL(source))
	if err := repo.create(t.Context()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := repo.fetch(t.Context(), nil, "main", 0); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	commit, err := repo.resolve(t.Context(), "main")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	writeFiles(t, filepath.Dir(file), map[string]string{"file": ""})
	if err := repo.checkout(t.Context(), commit, filepath.Join(file, "tree")); err == nil {
		t.Error("checkout answered no error under a file")
	}
	if err := repo.checkout(t.Context(), "0000000000000000000000000000000000000000",
		filepath.Join(t.TempDir(), "tree")); err == nil {
		t.Error("checkout answered no error for a commit the store does not hold")
	}
}

func TestTheLockIsPerRepository(t *testing.T) {
	holder := newStore(t.TempDir())
	one := holder.repository("https://example.com/one.git")
	two := holder.repository("https://example.com/two.git")

	release := one.lock()
	// Another URL takes its own lock while the first is held.
	holder.repository("https://example.com/two.git").lock()()
	two.lock()()

	taken := make(chan struct{})
	go func() {
		defer close(taken)
		holder.repository("https://example.com/one.git").lock()()
	}()
	select {
	case <-taken:
		t.Fatal("the same repository was locked twice at once")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-taken:
	case <-time.After(5 * time.Second):
		t.Fatal("the lock was not taken after it was released")
	}
}

func TestReplaceTreeMakesThePublishedTreeMatchTheFreshOne(t *testing.T) {
	for _, c := range []struct {
		name      string
		published map[string]string
		fresh     map[string]string
	}{
		{
			name:      "a file changes",
			published: map[string]string{"a.txt": "one"},
			fresh:     map[string]string{"a.txt": "two"},
		},
		{
			name:      "a file arrives",
			published: map[string]string{"a.txt": "one"},
			fresh:     map[string]string{"a.txt": "one", "b.txt": "two"},
		},
		{
			name:      "a file goes",
			published: map[string]string{"a.txt": "one", "b.txt": "two"},
			fresh:     map[string]string{"a.txt": "one"},
		},
		{
			name:      "a directory changes under a name that stays",
			published: map[string]string{"docs/a.txt": "one", "docs/b.txt": "two"},
			fresh:     map[string]string{"docs/a.txt": "one", "docs/c.txt": "three"},
		},
		{
			name:      "a file becomes a directory",
			published: map[string]string{"thing": "one"},
			fresh:     map[string]string{"thing/a.txt": "two"},
		},
		{
			name:      "a directory becomes a file",
			published: map[string]string{"thing/a.txt": "one"},
			fresh:     map[string]string{"thing": "two"},
		},
		{
			name:      "nothing changes",
			published: map[string]string{"a.txt": "one"},
			fresh:     map[string]string{"a.txt": "one"},
		},
		{
			name:      "the tree empties",
			published: map[string]string{"a.txt": "one"},
			fresh:     map[string]string{},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			published := filepath.Join(dir, "tree")
			fresh := filepath.Join(dir, "next")
			for _, made := range []string{published, fresh} {
				if err := os.MkdirAll(made, 0o755); err != nil {
					t.Fatalf("making %s: %v", made, err)
				}
			}
			writeFiles(t, published, c.published)
			writeFiles(t, fresh, c.fresh)

			if err := replaceTree(fresh, published); err != nil {
				t.Fatalf("replaceTree: %v", err)
			}
			if got := readTree(t, published); !sameTree(got, c.fresh) {
				t.Errorf("the published tree holds %v, want %v", got, c.fresh)
			}
		})
	}
}

// readTree is every file under dir by its path, so a test compares whole
// trees.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	found := map[string]string{}
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		found[filepath.ToSlash(name)] = string(content)
		return nil
	}); err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	return found
}

func sameTree(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for name, content := range want {
		if got[name] != content {
			return false
		}
	}
	return true
}

func TestReplaceTreeReportsATreeThatIsNotThere(t *testing.T) {
	dir := t.TempDir()
	if err := replaceTree(filepath.Join(dir, "gone"), dir); err == nil {
		t.Error("replaceTree answered no error for a fresh tree that is not there")
	}
	if err := replaceTree(dir, filepath.Join(dir, "gone")); err == nil {
		t.Error("replaceTree answered no error for a published tree that is not there")
	}
}

func TestTreeSizeAddsUpTheFiles(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a.txt": "12345", "docs/b.txt": "123"})
	size, err := treeSize(dir)
	if err != nil {
		t.Fatalf("treeSize: %v", err)
	}
	if size != 8 {
		t.Errorf("treeSize answered %d, want 8", size)
	}
}

func TestTreeSizeReportsATreeThatIsNotThere(t *testing.T) {
	if _, err := treeSize(filepath.Join(t.TempDir(), "gone")); err == nil {
		t.Error("treeSize answered no error for a tree that is not there")
	}
}

// readOnlyDir makes a directory that refuses writes, and skips the test
// under root, because root writes through the mode
func readOnlyDir(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("a mode holds nothing closed to root")
	}
	if err := os.Chmod(path, 0o500); err != nil {
		t.Fatalf("holding %s closed: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
}

func TestCreateReportsARepositoryGitCannotMake(t *testing.T) {
	holder, repo := storeWith(t, "https://example.com/data.git")
	if err := os.MkdirAll(repo.dir, 0o755); err != nil {
		t.Fatalf("making the directory: %v", err)
	}
	readOnlyDir(t, repo.dir)
	if err := holder.repository("https://example.com/data.git").create(t.Context()); err == nil {
		t.Error("create answered no error in a directory it cannot write")
	}
}

func TestReplaceTreeReportsWhatItCannotChange(t *testing.T) {
	for _, c := range []struct {
		name      string
		published map[string]string
		fresh     map[string]string
		closed    string
	}{
		{
			name:      "a file it cannot remove",
			published: map[string]string{"stale.txt": "one"},
			fresh:     map[string]string{},
			closed:    "",
		},
		{
			name:      "a file under a directory it cannot write",
			published: map[string]string{"docs/stale.txt": "one", "docs/a.txt": "two"},
			fresh:     map[string]string{"docs/a.txt": "two"},
			closed:    "docs",
		},
		{
			name:      "a directory it cannot replace with a file",
			published: map[string]string{"thing/a.txt": "one"},
			fresh:     map[string]string{"thing": "two"},
			closed:    "",
		},
		{
			name:      "a file it cannot rename over",
			published: map[string]string{"a.txt": "one"},
			fresh:     map[string]string{"a.txt": "two"},
			closed:    "",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			published := filepath.Join(dir, "tree")
			fresh := filepath.Join(dir, "next")
			for _, made := range []string{published, fresh} {
				if err := os.MkdirAll(made, 0o755); err != nil {
					t.Fatalf("making %s: %v", made, err)
				}
			}
			writeFiles(t, published, c.published)
			writeFiles(t, fresh, c.fresh)
			readOnlyDir(t, filepath.Join(published, c.closed))

			if err := replaceTree(fresh, published); err == nil {
				t.Error("replaceTree answered no error under a directory it cannot write")
			}
		})
	}
}
