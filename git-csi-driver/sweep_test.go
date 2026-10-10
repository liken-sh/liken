package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// sweepingNode is a node and a remote with one commit for it to
// stage.
func sweepingNode(t *testing.T, logs io.Writer) (*node, string) {
	t.Helper()
	answering, _ := testNode(t, logs)
	return answering, bareRemote(t, map[string]string{"a.txt": "one"})
}

// orphanRepository leaves the bare repository of the remote with no
// volume and no work tree that names it, which is the store after a
// read-only volume's unstage.
func orphanRepository(t *testing.T, answering *node, remote string) *repository {
	t.Helper()
	held := unstagedVolume(t, answering, "config", fileURL(remote))
	if err := os.RemoveAll(held.directory); err != nil {
		t.Fatalf("removing the work tree: %v", err)
	}
	return answering.store.repository(fileURL(remote))
}

func TestTheSweepKeepsARepositoryAVolumeUses(t *testing.T) {
	answering, remote := sweepingNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	// A read-only volume names its repository through the set the
	// node holds, because its directory carries no work tree.
	published := publishedVolume(t, answering, "data", fileURL(source), nil)
	orphanRepository(t, answering, remote)

	answering.sweepStore(t.Context())

	if _, err := os.Stat(answering.store.repository(published.attributes.url).dir); err != nil {
		t.Errorf("the repository of a published volume went: %v", err)
	}
}

func TestTheSweepReportsWhatItCannotRemove(t *testing.T) {
	logs := &logbook{}
	answering, remote := sweepingNode(t, logs)
	repo := orphanRepository(t, answering, remote)
	readOnlyDir(t, filepath.Dir(repo.dir))

	answering.sweepStore(t.Context())

	if !strings.Contains(logs.String(), "the repository stayed") {
		t.Errorf("the log is %q, want the failure in it", logs)
	}
}

// storeRefs is every ref the bare repository holds in the driver's own
// namespace, read with the tests' own git.
func storeRefs(t *testing.T, repo *repository) string {
	t.Helper()
	listed := git(t, repo.dir, "for-each-ref", "--format=%(refname)", refPrefix)
	return strings.Join(strings.Fields(listed), " ")
}

func TestTheSweepDeletesTheRefsNoVolumeFollows(t *testing.T) {
	logs := &logbook{}
	answering, remote := sweepingNode(t, logs)
	git(t, remote, "branch", "v1", "main")
	publishedVolume(t, answering, "data", fileURL(remote), nil)
	publishedVolume(t, answering, "docs", fileURL(remote), map[string]string{"ref": "v1"})
	repo := answering.store.repository(fileURL(remote))
	// The ref a volume followed until its claim went, which nothing on
	// this node names now.
	git(t, repo.dir, "update-ref", refPrefix+"gone", refPrefix+"main")

	answering.sweepStore(t.Context())

	want := refPrefix + "main " + refPrefix + "v1"
	if got := storeRefs(t, repo); got != want {
		t.Errorf("the repository holds %q, want %q", got, want)
	}
	if !strings.Contains(logs.String(), "swept the ref") {
		t.Errorf("the log is %q, want the ref it deleted in it", logs)
	}
}

func TestTheSweepKeepsTheRefAWorkTreeInTheStoreFollows(t *testing.T) {
	for _, c := range []struct {
		name  string
		stage func(t *testing.T, answering *node, url string)
	}{
		// A stage that was never published leaves a work tree and no
		// record, so HEAD is the whole evidence of the ref it follows.
		{name: "unstaged", stage: func(t *testing.T, answering *node, url string) {
			unstagedVolume(t, answering, "config", url)
		}},
		{name: "staged", stage: func(t *testing.T, answering *node, url string) {
			stagedVolume(t, answering, "config", url)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, remote := sweepingNode(t, io.Discard)
			c.stage(t, answering, fileURL(remote))
			repo := answering.store.repository(fileURL(remote))

			answering.sweepStore(t.Context())

			if got := storeRefs(t, repo); got != refPrefix+"main" {
				t.Errorf("the repository holds %q, want %q", got, refPrefix+"main")
			}
		})
	}
}

func TestTheSweepReportsARefItCannotDelete(t *testing.T) {
	logs := &logbook{}
	answering, remote := sweepingNode(t, logs)
	unstagedVolume(t, answering, "config", fileURL(remote))
	repo := answering.store.repository(fileURL(remote))
	git(t, repo.dir, "update-ref", refPrefix+"gone", refPrefix+"main")
	readOnlyDir(t, repo.dir)

	answering.sweepStore(t.Context())

	if !strings.Contains(logs.String(), "the ref stayed") {
		t.Errorf("the log is %q, want the ref it could not delete in it", logs)
	}
}

// collectable makes the repository one that git gc --auto decides to
// work on: a second pack, and a limit of one pack.
func collectable(t *testing.T, repo *repository, remote string) {
	t.Helper()
	git(t, repo.dir, "repack", "--quiet", "-d")
	remoteCommit(t, remote, map[string]string{"b.txt": "two"})
	git(t, repo.dir, "fetch", "--quiet", "--no-tags", remote, "+main:"+refPrefix+"main")
	git(t, repo.dir, "repack", "--quiet", "-d")
	git(t, repo.dir, "config", "gc.autoPackLimit", "1")
}

// unreachableObject writes an object no ref names, aged through the
// mtime that git's prune reads.
func unreachableObject(t *testing.T, repo *repository, content string, age time.Duration) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "object")
	if err := os.WriteFile(source, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the object: %v", err)
	}
	name := trimLine(git(t, repo.dir, "hash-object", "-w", "--", source))
	path := filepath.Join(repo.dir, "objects", name[:2], name[2:])
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("aging the object: %v", err)
	}
	return name
}

// holdsObject answers whether the repository still holds the object,
// loose or in a pack. The answer no is an answer, not a failure.
func holdsObject(t *testing.T, repo *repository, name string) bool {
	t.Helper()
	command := exec.Command("git", "cat-file", "-e", name)
	command.Dir = repo.dir
	command.Env = gitEnvironment()
	return command.Run() == nil
}

func TestTheSweepCollectsTheRepositoryThatStays(t *testing.T) {
	answering, remote := sweepingNode(t, io.Discard)
	publishedVolume(t, answering, "data", fileURL(remote), nil)
	repo := answering.store.repository(fileURL(remote))
	collectable(t, repo, remote)
	// git prunes an object no ref names once it is older than
	// gc.pruneExpire, two weeks by default.
	stale := unreachableObject(t, repo, "stale", 15*24*time.Hour)
	fresh := unreachableObject(t, repo, "fresh", 13*24*time.Hour)

	answering.sweepStore(t.Context())

	if holdsObject(t, repo, stale) {
		t.Error("an unreachable object older than git's prune age stayed")
	}
	if !holdsObject(t, repo, fresh) {
		t.Error("an unreachable object younger than git's prune age went")
	}
}

func TestTheSweepReportsARepositoryItCannotCollect(t *testing.T) {
	logs := &logbook{}
	answering, remote := sweepingNode(t, logs)
	unstagedVolume(t, answering, "config", fileURL(remote))
	repo := answering.store.repository(fileURL(remote))
	// A repository whose objects directory is a file is one git reads as
	// no repository at all.
	if err := os.RemoveAll(filepath.Join(repo.dir, "objects")); err != nil {
		t.Fatalf("removing the objects directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo.dir, "objects"), nil, 0o600); err != nil {
		t.Fatalf("writing the objects file: %v", err)
	}

	answering.sweepStore(t.Context())

	if !strings.Contains(logs.String(), "the repository was not collected") {
		t.Errorf("the log is %q, want the repository it could not collect in it", logs)
	}
}

func TestTheSweepReadsAStoreThatIsNotThere(t *testing.T) {
	answering, _ := sweepingNode(t, io.Discard)
	// A driver that has held no volume has no volumes directory
	// and no repos directory, and the sweep is a walk of both.
	answering.sweepStore(t.Context())

	// A bare repository with no volumes directory beside it is
	// what a store holds after a person removed the work trees.
	orphan := filepath.Join(answering.store.root, "repos", "e20c859f")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatalf("making the repository directory: %v", err)
	}

	answering.sweepStore(t.Context())

	if _, err := os.Stat(orphan); err == nil {
		t.Error("the repository no work tree names stayed")
	}
}

func TestTheSweepRunsOnItsIntervalUntilTheDriverStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, remote := sweepingNode(t, io.Discard)
		answering.sweepEvery = 10 * time.Millisecond
		repo := orphanRepository(t, answering, remote)

		ctx, stop := context.WithCancel(t.Context())
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			answering.sweeping(ctx)
		}()

		time.Sleep(answering.sweepEvery)
		synctest.Wait()
		if _, err := os.Stat(repo.dir); err == nil {
			t.Error("the loop swept nothing on its interval")
		}
		// A loop that outlives its context blocks the bubble for good,
		// and synctest fails the test.
		stop()
		<-stopped
	})
}
