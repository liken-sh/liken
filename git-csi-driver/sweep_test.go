package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

// unstagedVolume stages a writeable volume and unstages it, which
// is the state every work tree the sweep looks at is in.
func unstagedVolume(t *testing.T, answering *node, id, url string) *volume {
	t.Helper()
	held, request := stagedVolume(t, answering, id, url)
	if _, err := answering.NodeUnstageVolume(t.Context(), &csi.NodeUnstageVolumeRequest{
		VolumeId: id, StagingTargetPath: request.GetStagingTargetPath(),
	}); err != nil {
		t.Fatalf("NodeUnstageVolume: %v", err)
	}
	return held
}

// unstagedAgo backdates the unstage record, which is the only
// evidence the sweep has of how long nothing has staged the volume.
func unstagedAgo(t *testing.T, answering *node, id string, ago time.Duration) {
	t.Helper()
	path := filepath.Join(answering.store.volumeDir(id), unstagedFile)
	when := time.Now().Add(-ago).UTC().Format(time.RFC3339) + "\n"
	if err := os.WriteFile(path, []byte(when), 0o600); err != nil {
		t.Fatalf("writing the unstage record: %v", err)
	}
}

// sweepingNode is a node that removes a work tree an hour after
// the unstage, so a test states an age instead of waiting for one.
func sweepingNode(t *testing.T, logs io.Writer) (*node, string) {
	t.Helper()
	answering, _ := testNode(t, logs)
	answering.sweepAfter = time.Hour
	return answering, bareRemote(t, map[string]string{"a.txt": "one"})
}

func TestTheUnstageRecordsWhenTheNodeLastHeldTheVolume(t *testing.T) {
	answering, remote := sweepingNode(t, io.Discard)
	unstagedVolume(t, answering, "config", fileURL(remote))

	when, found := unstagedAt(answering.store.volumeDir("config"))
	if !found {
		t.Fatal("the unstage left no record")
	}
	if time.Since(when) > time.Minute {
		t.Errorf("the record says %s, want the moment of the unstage", when)
	}
}

func TestTheSweepRemovesAWorkTreeNothingStages(t *testing.T) {
	logs := &logbook{}
	answering, remote := sweepingNode(t, logs)
	unstagedVolume(t, answering, "config", fileURL(remote))
	unstagedAgo(t, answering, "config", 2*time.Hour)

	answering.sweepStore(t.Context())

	if _, err := os.Stat(answering.store.volumeDir("config")); err == nil {
		t.Error("the work tree stayed")
	}
	if _, err := os.Stat(answering.store.repository(fileURL(remote)).dir); err == nil {
		t.Error("the bare repository stayed after its last work tree went")
	}
	if !strings.Contains(logs.String(), "swept the work tree") ||
		!strings.Contains(logs.String(), "swept the repository") {
		t.Errorf("the log is %q, want both removals in it", logs)
	}
}

func TestTheSweepKeepsWhatItMustNotRemove(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, answering *node, held *volume)
	}{
		{
			name:  "a volume this node still holds",
			stand: func(t *testing.T, answering *node, held *volume) { restage(t, answering, held) },
		},
		{
			name:  "a volume no unstage has left a record for",
			stand: func(t *testing.T, answering *node, held *volume) { removeRecord(t, held) },
		},
		{
			name: "a volume unstaged a moment ago",
			stand: func(t *testing.T, answering *node, _ *volume) {
				unstagedAgo(t, answering, "config", time.Minute)
			},
		},
		{
			name: "a work tree with commits the remote does not hold",
			stand: func(t *testing.T, answering *node, held *volume) {
				driverCommit(t, answering, held, map[string]string{"c.txt": "three"})
			},
		},
		{
			name: "a work tree on its side branch",
			stand: func(t *testing.T, _ *node, held *volume) {
				if err := held.work.markDiverged(t.Context(), "main.config"); err != nil {
					t.Fatalf("markDiverged: %v", err)
				}
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, remote := sweepingNode(t, io.Discard)
			held := unstagedVolume(t, answering, "config", fileURL(remote))
			unstagedAgo(t, answering, "config", 2*time.Hour)
			c.stand(t, answering, held)

			answering.sweepStore(t.Context())

			if _, err := os.Stat(held.directory); err != nil {
				t.Errorf("the work tree went: %v", err)
			}
			if _, err := os.Stat(answering.store.repository(fileURL(remote)).dir); err != nil {
				t.Errorf("the bare repository went: %v", err)
			}
		})
	}
}

// restage puts the volume back in the set this node holds, which
// is the state the sweep never touches.
func restage(t *testing.T, answering *node, held *volume) {
	t.Helper()
	answering.mu.Lock()
	defer answering.mu.Unlock()
	answering.staged[held.id] = held
}

// removeRecord takes the unstage record away, which is a work
// tree a driver that was killed left.
func removeRecord(t *testing.T, held *volume) {
	t.Helper()
	if err := os.Remove(filepath.Join(held.directory, unstagedFile)); err != nil {
		t.Fatalf("removing the unstage record: %v", err)
	}
}

func TestTheSweepNamesAnAbandonedWorkTreeInTheNextStage(t *testing.T) {
	answering, remote := sweepingNode(t, io.Discard)
	held := unstagedVolume(t, answering, "config", fileURL(remote))
	unstagedAgo(t, answering, "config", 30*time.Hour)
	driverCommit(t, answering, held, map[string]string{"c.txt": "three"})
	answering.sweepStore(t.Context())

	arriving, _ := stagedVolume(t, answering, "config-again", fileURL(remote))

	abnormal, message := arriving.report()
	if !abnormal || !strings.Contains(message, "config") || !strings.Contains(message, "30h") {
		t.Errorf("the condition says %v, %q, want the abandoned tree and its age", abnormal, message)
	}
}

func TestAnAbandonedWorkTreeOfAnotherRepositoryIsNotNamed(t *testing.T) {
	answering, remote := sweepingNode(t, io.Discard)
	held := unstagedVolume(t, answering, "config", fileURL(remote))
	unstagedAgo(t, answering, "config", 30*time.Hour)
	driverCommit(t, answering, held, map[string]string{"c.txt": "three"})
	answering.sweepStore(t.Context())

	elsewhere := bareRemote(t, map[string]string{"a.txt": "one"})
	arriving, _ := stagedVolume(t, answering, "other", fileURL(elsewhere))

	if abnormal, message := arriving.report(); abnormal {
		t.Errorf("a volume of another repository reported %q", message)
	}
}

func TestAnAbandonedWorkTreeWithNoRepositoryIsOnlyLogged(t *testing.T) {
	logs := &logbook{}
	answering, remote := sweepingNode(t, logs)
	held := unstagedVolume(t, answering, "config", fileURL(remote))
	unstagedAgo(t, answering, "config", 2*time.Hour)
	driverCommit(t, answering, held, map[string]string{"c.txt": "three"})
	if err := os.Remove(filepath.Join(held.work.gitDir, alternatesFile)); err != nil {
		t.Fatalf("removing the alternates file: %v", err)
	}

	answering.sweepStore(t.Context())

	if !strings.Contains(logs.String(), "the work tree holds unpushed commits") {
		t.Errorf("the log is %q, want the tree it kept in it", logs)
	}
	answering.mu.Lock()
	defer answering.mu.Unlock()
	if len(answering.abandoned) != 0 {
		t.Errorf("the driver holds %v, want no repository named", answering.abandoned)
	}
}

func TestTheSweepKeepsARepositoryAVolumeUses(t *testing.T) {
	answering, remote := sweepingNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	// A read-only volume names its repository through the set the
	// node holds, because its directory carries no work tree.
	published := publishedVolume(t, answering, "data", fileURL(source), nil)
	unstagedVolume(t, answering, "config", fileURL(remote))
	unstagedAgo(t, answering, "config", 2*time.Hour)

	answering.sweepStore(t.Context())

	if _, err := os.Stat(answering.store.repository(published.attributes.url).dir); err != nil {
		t.Errorf("the repository of a published volume went: %v", err)
	}
}

func TestTheSweepReportsWhatItCannotRemove(t *testing.T) {
	logs := &logbook{}
	answering, remote := sweepingNode(t, logs)
	held := unstagedVolume(t, answering, "config", fileURL(remote))
	unstagedAgo(t, answering, "config", 2*time.Hour)
	readOnlyDir(t, filepath.Dir(held.directory))
	readOnlyDir(t, filepath.Dir(answering.store.repository(fileURL(remote)).dir))

	answering.sweepStore(t.Context())

	if !strings.Contains(logs.String(), "the work tree stayed") ||
		!strings.Contains(logs.String(), "the repository stayed") {
		t.Errorf("the log is %q, want both failures in it", logs)
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
	answering, remote := sweepingNode(t, io.Discard)
	// A stage that was never published leaves a work tree and no record,
	// so HEAD is the whole evidence of the ref it follows.
	unstagedVolume(t, answering, "config", fileURL(remote))
	repo := answering.store.repository(fileURL(remote))

	answering.sweepStore(t.Context())

	if got := storeRefs(t, repo); got != refPrefix+"main" {
		t.Errorf("the repository holds %q, want %q", got, refPrefix+"main")
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
	stale := unreachableObject(t, repo, "stale", 2*time.Hour)
	fresh := unreachableObject(t, repo, "fresh", 0)

	answering.sweepStore(t.Context())

	if holdsObject(t, repo, stale) {
		t.Error("an unreachable object older than the sweep age stayed")
	}
	if !holdsObject(t, repo, fresh) {
		t.Error("an unreachable object younger than the sweep age went")
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

func TestTheSweepPassesOverWhatIsNotAWorkTree(t *testing.T) {
	answering, _ := sweepingNode(t, io.Discard)
	directory := answering.store.volumeDir("data")
	if err := os.MkdirAll(filepath.Join(directory, "git"), 0o755); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	unstagedAgo(t, answering, "data", 2*time.Hour)

	answering.sweepStore(t.Context())

	if _, err := os.Stat(directory); err != nil {
		t.Errorf("a directory that is no work tree went: %v", err)
	}
}

func TestTheSweepPassesOverAGitDirectoryWithNoCommit(t *testing.T) {
	answering, _ := sweepingNode(t, io.Discard)
	directory := answering.store.volumeDir("config")
	work := answering.store.tree("config")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("making the volume directory: %v", err)
	}
	if _, err := work.git(t.Context(), "init", "--quiet"); err != nil {
		t.Fatalf("making the git directory: %v", err)
	}
	unstagedAgo(t, answering, "config", 2*time.Hour)

	answering.sweepStore(t.Context())

	if _, err := os.Stat(directory); err != nil {
		t.Errorf("a git directory with no commit went: %v", err)
	}
}

func TestASweptVolumeWhoseClaimStandsPostsTheEvent(t *testing.T) {
	answering, remote := sweepingNode(t, io.Discard)
	unstagedVolume(t, answering, "config", fileURL(remote))
	unstagedAgo(t, answering, "config", 2*time.Hour)
	boundVolume(t, answering, "config", "")

	answering.sweepStore(t.Context())

	if got := reasonsOf(t, answering); !strings.Contains(got, reasonSwept) {
		t.Errorf("the events are %q, want %s in them", got, reasonSwept)
	}
}

func TestASweptVolumeWithNoClaimPostsNothing(t *testing.T) {
	logs := &logbook{}
	answering, remote := sweepingNode(t, logs)
	unstagedVolume(t, answering, "config", fileURL(remote))
	unstagedAgo(t, answering, "config", 2*time.Hour)

	answering.sweepStore(t.Context())

	if got := reasonsOf(t, answering); strings.Contains(got, reasonSwept) {
		t.Errorf("the events are %q, want no %s in them", got, reasonSwept)
	}
	if !strings.Contains(logs.String(), "the swept volume names no claim") {
		t.Errorf("the log is %q, want the claim it could not find in it", logs)
	}
}

func TestADriverOutsideAClusterSweepsAndPostsNothing(t *testing.T) {
	answering, remote := sweepingNode(t, io.Discard)
	answering.arms.client = nil
	unstagedVolume(t, answering, "config", fileURL(remote))
	unstagedAgo(t, answering, "config", 2*time.Hour)

	answering.sweepStore(t.Context())

	if _, err := os.Stat(answering.store.volumeDir("config")); err == nil {
		t.Error("the work tree stayed")
	}
}

func TestTheSweepRunsOnItsIntervalUntilTheDriverStops(t *testing.T) {
	answering, remote := sweepingNode(t, io.Discard)
	answering.sweepEvery = 10 * time.Millisecond
	unstagedVolume(t, answering, "config", fileURL(remote))
	unstagedAgo(t, answering, "config", 2*time.Hour)

	ctx, stop := context.WithCancel(t.Context())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		answering.sweeping(ctx)
	}()

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(answering.store.volumeDir("config")); err != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(answering.store.volumeDir("config")); err == nil {
		t.Error("the loop swept nothing within 30s")
	}
	stop()
	select {
	case <-stopped:
	case <-time.After(30 * time.Second):
		t.Error("the loop did not stop within 30s")
	}
}

func TestTheAgeOfAWorkTreeIsWholeHours(t *testing.T) {
	if got := age(time.Now().Add(-90 * time.Minute)); got != "1h" {
		t.Errorf("age answered %q, want 1h", got)
	}
}

func TestAnUnstageRecordTheDriverCannotRead(t *testing.T) {
	directory := t.TempDir()
	if _, found := unstagedAt(directory); found {
		t.Error("a directory with no record answered a time")
	}
	if err := os.WriteFile(filepath.Join(directory, unstagedFile), []byte("soon\n"), 0o600); err != nil {
		t.Fatalf("writing the record: %v", err)
	}
	if _, found := unstagedAt(directory); found {
		t.Error("a record that is no time answered a time")
	}
}

func TestAnUnstageRecordTheDriverCannotWrite(t *testing.T) {
	logs := &logbook{}
	answering, _ := testNode(t, logs)
	answering.markUnstaged(t.Context(), &volume{
		id:        "config",
		directory: filepath.Join(t.TempDir(), "gone"),
	})
	if !strings.Contains(logs.String(), "the unstage time was not written") {
		t.Errorf("the log is %q, want the record it could not write in it", logs)
	}
}
