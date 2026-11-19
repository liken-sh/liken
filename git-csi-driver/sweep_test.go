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

// sweepingNode returns a node and the path of a bare remote with one
// commit, for the node to stage.
func sweepingNode(t *testing.T, logs io.Writer) (*node, string) {
	t.Helper()
	n, _ := testNode(t, logs)
	return n, bareRemote(t, map[string]string{"a.txt": "one"})
}

// orphanRepository stages and unstages a writeable volume of the remote,
// then deletes the volume's directory. That leaves a bare repository
// that no volume and no work tree refers to, as after a read-only
// volume is unstaged.
func orphanRepository(t *testing.T, n *node, remote string) *repository {
	t.Helper()
	vol := unstagedVolume(t, n, "config", fileURL(remote))
	if err := os.RemoveAll(vol.directory); err != nil {
		t.Fatalf("removing the work tree: %v", err)
	}
	return n.store.repository(fileURL(remote))
}

func TestTheSweepKeepsARepositoryAVolumeUses(t *testing.T) {
	n, remote := sweepingNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	// A read-only volume has no work tree, so usesRepository finds it
	// only in the node's in-memory set.
	published := publishedVolume(t, n, "data", fileURL(source), nil)
	orphanRepository(t, n, remote)

	n.sweepStore(t.Context())

	if _, err := os.Stat(n.store.repository(published.attributes.url).dir); err != nil {
		t.Errorf("the repository of a published volume is missing after the sweep: %v", err)
	}
}

func TestTheSweepReportsWhatItCannotRemove(t *testing.T) {
	logs := &logbook{}
	n, remote := sweepingNode(t, logs)
	repo := orphanRepository(t, n, remote)
	readOnlyDir(t, filepath.Dir(repo.dir))

	n.sweepStore(t.Context())

	if !strings.Contains(logs.String(), "The repository directory was not removed") {
		t.Errorf("the log is %q, want the line for the repository directory that was not removed", logs)
	}
}

// storeRefs returns the refs under refPrefix in the bare repository,
// read with the test's own git command.
func storeRefs(t *testing.T, repo *repository) string {
	t.Helper()
	listed := git(t, repo.dir, "for-each-ref", "--format=%(refname)", refPrefix)
	return strings.Join(strings.Fields(listed), " ")
}

func TestTheSweepDeletesTheRefsNoVolumeFollows(t *testing.T) {
	logs := &logbook{}
	n, remote := sweepingNode(t, logs)
	git(t, remote, "branch", "v1", "main")
	publishedVolume(t, n, "data", fileURL(remote), nil)
	publishedVolume(t, n, "docs", fileURL(remote), map[string]string{"ref": "v1"})
	repo := n.store.repository(fileURL(remote))
	// A ref that a volume followed until its claim was deleted.
	git(t, repo.dir, "update-ref", refPrefix+"gone", refPrefix+"main")

	n.sweepStore(t.Context())

	want := refPrefix + "main " + refPrefix + "v1"
	if got := storeRefs(t, repo); got != want {
		t.Errorf("the repository has %q, want %q", got, want)
	}
	if !strings.Contains(logs.String(), "Deleted the ref that no volume follows") {
		t.Errorf("the log is %q, want the line for the deleted ref", logs)
	}
}

func TestTheSweepKeepsTheRefAWorkTreeInTheStoreFollows(t *testing.T) {
	for _, c := range []struct {
		name  string
		stage func(t *testing.T, n *node, url string)
	}{
		// An unstaged writeable volume that was never published leaves a
		// work tree and no record, and the driver holds no volume for it,
		// so the work tree's HEAD is the only place the ref is recorded.
		{name: "unstaged", stage: func(t *testing.T, n *node, url string) {
			unstagedVolume(t, n, "config", url)
		}},
		{name: "staged", stage: func(t *testing.T, n *node, url string) {
			stagedVolume(t, n, "config", url)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, remote := sweepingNode(t, io.Discard)
			c.stage(t, n, fileURL(remote))
			repo := n.store.repository(fileURL(remote))

			n.sweepStore(t.Context())

			if got := storeRefs(t, repo); got != refPrefix+"main" {
				t.Errorf("the repository has %q, want %q", got, refPrefix+"main")
			}
		})
	}
}

func TestTheSweepReportsARefItCannotDelete(t *testing.T) {
	logs := &logbook{}
	n, remote := sweepingNode(t, logs)
	unstagedVolume(t, n, "config", fileURL(remote))
	repo := n.store.repository(fileURL(remote))
	git(t, repo.dir, "update-ref", refPrefix+"gone", refPrefix+"main")
	readOnlyDir(t, repo.dir)

	n.sweepStore(t.Context())

	if !strings.Contains(logs.String(), "The ref was not deleted from the repository") {
		t.Errorf("the log is %q, want the line for the ref that was not deleted", logs)
	}
}

// collectable makes `git gc --auto` act on the repository, by adding a
// second pack and setting the pack limit to one.
func collectable(t *testing.T, repo *repository, remote string) {
	t.Helper()
	git(t, repo.dir, "repack", "--quiet", "-d")
	remoteCommit(t, remote, map[string]string{"b.txt": "two"})
	git(t, repo.dir, "fetch", "--quiet", "--no-tags", remote, "+main:"+refPrefix+"main")
	git(t, repo.dir, "repack", "--quiet", "-d")
	git(t, repo.dir, "config", "gc.autoPackLimit", "1")
}

// unreachableObject writes an object that no ref reaches, and sets its
// mtime to the given age, which is what git prune reads.
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

// holdsObject reports whether the repository still has the object,
// loose or in a pack. A missing object is a false result, not a failure.
func holdsObject(t *testing.T, repo *repository, name string) bool {
	t.Helper()
	command := exec.Command("git", "cat-file", "-e", name)
	command.Dir = repo.dir
	command.Env = gitEnvironment()
	return command.Run() == nil
}

func TestTheSweepCollectsTheRepositoryThatStays(t *testing.T) {
	n, remote := sweepingNode(t, io.Discard)
	publishedVolume(t, n, "data", fileURL(remote), nil)
	repo := n.store.repository(fileURL(remote))
	collectable(t, repo, remote)
	// git prunes an unreachable object once it is older than
	// gc.pruneExpire, two weeks by default.
	stale := unreachableObject(t, repo, "stale", 15*24*time.Hour)
	fresh := unreachableObject(t, repo, "fresh", 13*24*time.Hour)

	n.sweepStore(t.Context())

	if holdsObject(t, repo, stale) {
		t.Error("an unreachable object older than git's prune age is still there after the sweep, want it deleted")
	}
	if !holdsObject(t, repo, fresh) {
		t.Error("an unreachable object younger than git's prune age is gone after the sweep, want it kept")
	}
}

func TestTheSweepReportsARepositoryItCannotCollect(t *testing.T) {
	logs := &logbook{}
	n, remote := sweepingNode(t, logs)
	unstagedVolume(t, n, "config", fileURL(remote))
	repo := n.store.repository(fileURL(remote))
	// When the objects directory is a file, git does not treat the
	// directory as a repository.
	if err := os.RemoveAll(filepath.Join(repo.dir, "objects")); err != nil {
		t.Fatalf("removing the objects directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo.dir, "objects"), nil, 0o600); err != nil {
		t.Fatalf("writing the objects file: %v", err)
	}

	n.sweepStore(t.Context())

	if !strings.Contains(logs.String(), "git gc did not finish on the repository") {
		t.Errorf("the log is %q, want the line for the repository that gc did not finish on", logs)
	}
}

func TestTheSweepReadsAStoreThatIsNotThere(t *testing.T) {
	n, _ := sweepingNode(t, io.Discard)
	// A driver that has never held a volume has no volumes directory
	// and no repos directory, and the sweep reads both.
	n.sweepStore(t.Context())

	// A bare repository with no volumes directory is what the store
	// holds after a person removes the work trees.
	orphan := filepath.Join(n.store.root, "repos", "e20c859f")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatalf("making the repository directory: %v", err)
	}

	n.sweepStore(t.Context())

	if _, err := os.Stat(orphan); err == nil {
		t.Error("the repository that no work tree names is still there after the sweep, want it deleted")
	}
}

func TestTheSweepKeepsTheRepositoryWhenItCannotReadTheVolumes(t *testing.T) {
	n, remote := sweepingNode(t, io.Discard)
	// The driver holds no volume for this work tree, so only the
	// alternates file on disk records that the tree uses the repository.
	unstagedVolume(t, n, "config", fileURL(remote))
	repo := n.store.repository(fileURL(remote))
	git(t, repo.dir, "update-ref", refPrefix+"kept", refPrefix+"main")
	unreadableDir(t, filepath.Join(n.store.root, "volumes"))

	n.sweepStore(t.Context())

	if _, err := os.Stat(repo.dir); err != nil {
		t.Fatalf("the repository is gone after a sweep that could not read the volumes directory: %v, want it kept because a work tree can still use it", err)
	}
	if got := storeRefs(t, repo); got != refPrefix+"kept "+refPrefix+"main" {
		t.Errorf("the repository has %q, want %q because the sweep cannot tell which refs the volumes follow", got, refPrefix+"kept "+refPrefix+"main")
	}
}

// unreadableAlternates removes every permission from the alternates file
// of the volume's work tree. Root ignores the mode, so the test is
// skipped when it runs as root.
func unreadableAlternates(t *testing.T, vol *volume) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root can read a file whatever its mode")
	}
	path := filepath.Join(vol.directory, "git", alternatesFile)
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatalf("removing the permissions of %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
}

func TestTheSweepKeepsTheRepositoryOfAWorkTreeItCannotRead(t *testing.T) {
	logs := &logbook{}
	n, remote := sweepingNode(t, logs)
	vol := unstagedVolume(t, n, "config", fileURL(remote))
	repo := n.store.repository(fileURL(remote))
	unreadableAlternates(t, vol)

	n.sweepStore(t.Context())

	if _, err := os.Stat(repo.dir); err != nil {
		t.Errorf("the repository is gone after a sweep that could not read a work tree's alternates file: %v, want it kept", err)
	}
	if !strings.Contains(logs.String(), "The repository was not swept, because the store was not read") {
		t.Errorf("the log is %q, want the line for the repository that was not swept", logs)
	}
}

// A volume in memory uses the repository, so the sweep keeps it without a
// read of the store, and reads the store only for the refs.
func TestTheSweepKeepsTheRefsWhenItCannotReadTheStore(t *testing.T) {
	for _, c := range []struct {
		name   string
		unread func(t *testing.T, n *node, remote string)
	}{
		{name: "the volumes directory", unread: func(t *testing.T, n *node, _ string) {
			unreadableDir(t, filepath.Join(n.store.root, "volumes"))
		}},
		{name: "the alternates file of a work tree", unread: func(t *testing.T, n *node, remote string) {
			unreadableAlternates(t, unstagedVolume(t, n, "config", fileURL(remote)))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			logs := &logbook{}
			n, remote := sweepingNode(t, logs)
			publishedVolume(t, n, "data", fileURL(remote), nil)
			repo := n.store.repository(fileURL(remote))
			git(t, repo.dir, "update-ref", refPrefix+"kept", refPrefix+"main")
			c.unread(t, n, remote)

			n.sweepStore(t.Context())

			if got := storeRefs(t, repo); got != refPrefix+"kept "+refPrefix+"main" {
				t.Errorf("the repository has %q, want %q because the sweep cannot tell which refs the volumes follow", got, refPrefix+"kept "+refPrefix+"main")
			}
			if !strings.Contains(logs.String(), "The refs of the repository were not swept, because the store was not read") {
				t.Errorf("the log is %q, want the line for the refs that were not swept", logs)
			}
		})
	}
}

func TestTheSweepKeepsTheRefsWhenItCannotReadTheRefOfAWorkTree(t *testing.T) {
	logs := &logbook{}
	n, remote := sweepingNode(t, logs)
	// The driver holds no volume for this work tree, so the tree's HEAD is
	// the only record of the ref it follows.
	unstagedVolume(t, n, "config", fileURL(remote))
	repo := n.store.repository(fileURL(remote))
	refusingGit(t, "symbolic-ref")

	n.sweepStore(t.Context())

	if got := storeRefs(t, repo); got != refPrefix+"main" {
		t.Errorf("the repository has %q after a sweep that could not read the work tree's HEAD, want %q because the tree may follow it", got, refPrefix+"main")
	}
	if !strings.Contains(logs.String(), "The refs of the repository were not swept, because the store was not read") {
		t.Errorf("the log is %q, want the line for the refs that were not swept", logs)
	}
}

func TestTheSweepKeepsTheRefsWhenItCannotReadARecord(t *testing.T) {
	logs := &logbook{}
	n, remote := sweepingNode(t, logs)
	publishedVolume(t, n, "data", fileURL(remote), nil)
	repo := n.store.repository(fileURL(remote))
	// A record that the driver cannot read can name any ref, such as the
	// ref of a read-only volume that the driver does not hold.
	git(t, repo.dir, "update-ref", refPrefix+"v1", refPrefix+"main")
	writeFiles(t, n.store.volumeDir("docs"), map[string]string{recordFile: "{}"})
	unreadableFile(t, filepath.Join(n.store.volumeDir("docs"), recordFile))

	n.sweepStore(t.Context())

	if got := storeRefs(t, repo); got != refPrefix+"main "+refPrefix+"v1" {
		t.Errorf("the repository has %q after a sweep that could not read a record, want %q because the record may follow v1", got, refPrefix+"main "+refPrefix+"v1")
	}
	if !strings.Contains(logs.String(), keptRefsLine) {
		t.Errorf("the log is %q, want the line for the refs that are kept", logs)
	}
}

// keptRefsLine is the message of the warning for a record that the
// sweep cannot read.
const keptRefsLine = "The refs of every repository are kept until the record of the volume can be read"

func TestARecordThatDoesNotParseIsReportedOnceForASweepWithItsPath(t *testing.T) {
	logs := &logbook{}
	n, remote := sweepingNode(t, logs)
	other := bareRemote(t, map[string]string{"b.txt": "two"})
	publishedVolume(t, n, "data", fileURL(remote), nil)
	publishedVolume(t, n, "docs", fileURL(other), nil)
	path := filepath.Join(n.store.volumeDir("torn"), recordFile)
	writeFiles(t, n.store.volumeDir("torn"), map[string]string{recordFile: "{"})

	n.sweepStore(t.Context())

	if got := strings.Count(logs.String(), keptRefsLine); got != 1 {
		t.Errorf("the sweep of two repositories logged %q %d times, want once for the sweep", keptRefsLine, got)
	}
	if !strings.Contains(logs.String(), path) {
		t.Errorf("the log is %q, want the path %s of the record that does not parse", logs, path)
	}
}

func TestTheFollowedRefsAreUnknownWhileARecordCannotBeRead(t *testing.T) {
	n, remote := sweepingNode(t, io.Discard)
	writeFiles(t, n.store.volumeDir("torn"), map[string]string{recordFile: "{"})

	if _, err := n.followedRefs(t.Context(), n.store.repository(fileURL(remote)).dir); err == nil {
		t.Error("followedRefs returned no error with a record that does not parse, want one because the record can follow any ref")
	}
}

func TestAFileInTheVolumesDirectoryIsNoVolume(t *testing.T) {
	n, remote := sweepingNode(t, io.Discard)
	repo := orphanRepository(t, n, remote)
	writeFiles(t, filepath.Join(n.store.root, "volumes"), map[string]string{"notes.txt": "left by a person"})

	n.sweepStore(t.Context())

	if _, err := os.Stat(repo.dir); err == nil {
		t.Error("the repository that no volume uses is still there after the sweep, want it deleted because a file in volumes/ holds no work tree")
	}
}

func TestTheSweepRunsOnItsIntervalUntilTheDriverStops(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, remote := sweepingNode(t, io.Discard)
		n.sweepEvery = 10 * time.Millisecond
		repo := orphanRepository(t, n, remote)

		ctx, stop := context.WithCancel(t.Context())
		stopped := make(chan struct{})
		go func() {
			defer close(stopped)
			n.sweepPeriodically(ctx)
		}()

		time.Sleep(n.sweepEvery)
		synctest.Wait()
		if _, err := os.Stat(repo.dir); err == nil {
			t.Error("the sweep loop did not run on its interval")
		}
		// A goroutine still blocked at the end of the bubble makes synctest
		// fail the test, so a loop that ignores its context fails here.
		stop()
		<-stopped
	})
}
