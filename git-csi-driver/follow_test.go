package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// publishedVolume publishes one inline volume on the node and returns
// what the node holds for it.
func publishedVolume(t *testing.T, answering *node, id, url string, extra map[string]string) *volume {
	t.Helper()
	request := publishRequest(t, id, url, extra)
	if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume %s: %v", id, err)
	}
	answering.mu.Lock()
	defer answering.mu.Unlock()
	return answering.volumes[id]
}

// followerOf is a follower of the URL with no loop running, so a test
// drives one pass itself.
func followerOf(answering *node, url string) *follower {
	return &follower{
		node:       answering,
		repository: answering.store.repository(url),
		wake:       make(chan struct{}, 1),
		volumes:    map[string]*volume{},
	}
}

// inodeOf is the inode of a directory, which is what a bind mount holds
// on to.
func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

func TestTheIntervalIsTheShortestPullOfTheVolumesThatShare(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)

	loop := followerOf(answering, url)
	if got := loop.interval(); got != defaultPull {
		t.Errorf("a follower of no volumes answered %v, want %v", got, defaultPull)
	}
	loop.add(&volume{id: "csi-1", attributes: &attributes{pull: time.Hour}})
	if got := loop.interval(); got != time.Hour {
		t.Errorf("the interval is %v, want %v", got, time.Hour)
	}
	loop.add(&volume{id: "csi-2", attributes: &attributes{pull: time.Minute}})
	if got := loop.interval(); got != time.Minute {
		t.Errorf("the interval is %v, want %v", got, time.Minute)
	}
	loop.remove(&volume{id: "csi-2", attributes: &attributes{pull: time.Minute}})
	if got := loop.interval(); got != time.Hour {
		t.Errorf("the interval is %v, want %v", got, time.Hour)
	}
}

func TestOneLoopFollowsARepositoryForEveryVolumeOfIt(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	name := answering.store.repository(url).name

	first := publishedVolume(t, answering, "csi-1", url, map[string]string{"pull": "1h"})
	second := publishedVolume(t, answering, "csi-2", url, map[string]string{"pull": "1h"})

	answering.mu.Lock()
	loop, found := answering.followers[name]
	answering.mu.Unlock()
	if !found {
		t.Fatal("no loop follows the repository")
	}
	if got := len(answering.followers); got != 1 {
		t.Errorf("the node holds %d loops, want 1", got)
	}
	if got := len(loop.snapshot()); got != 2 {
		t.Errorf("the loop follows %d volumes, want 2", got)
	}

	answering.mu.Lock()
	answering.unfollow(first)
	answering.mu.Unlock()
	answering.mu.Lock()
	stillThere := len(answering.followers)
	answering.mu.Unlock()
	if stillThere != 1 {
		t.Errorf("the loop stopped while a volume still followed the repository")
	}

	answering.mu.Lock()
	answering.unfollow(second)
	answering.mu.Unlock()
	answering.mu.Lock()
	gone := len(answering.followers)
	answering.mu.Unlock()
	if gone != 0 {
		t.Errorf("the node holds %d loops after the last volume went, want 0", gone)
	}
}

func TestAVolumeThatPullsNeverJoinsNoLoop(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	pinned := publishedVolume(t, answering, "csi-1", fileURL(source), map[string]string{"pull": "never"})

	answering.mu.Lock()
	loops := len(answering.followers)
	answering.unfollow(pinned)
	answering.mu.Unlock()
	if loops != 0 {
		t.Errorf("a volume that pulls never made %d loops, want 0", loops)
	}
}

func TestUnfollowPassesOverARepositoryWithNoLoop(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	answering.mu.Lock()
	defer answering.mu.Unlock()
	answering.unfollow(&volume{id: "csi-9", attributes: &attributes{pull: time.Hour, url: "file:///gone"}})
}

func TestRefreshMovesThePublishedTreeInsideTheDirectoryThePodHolds(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one", "docs/b.txt": "two"})
	url := fileURL(source)
	following := publishedVolume(t, answering, "csi-1", url, map[string]string{"pull": "1h"})
	before := inodeOf(t, following.tree)

	commitFiles(t, source, map[string]string{"a.txt": "three", "docs/c.txt": "four"})
	if err := os.Remove(filepath.Join(source, "docs", "b.txt")); err != nil {
		t.Fatalf("removing the file: %v", err)
	}
	commitFiles(t, source, nil)

	loop := followerOf(answering, url)
	loop.refresh(t.Context(), following)

	want := map[string]string{"a.txt": "three", "docs/c.txt": "four"}
	if got := readTree(t, following.tree); !sameTree(got, want) {
		t.Errorf("the tree holds %v, want %v", got, want)
	}
	if after := inodeOf(t, following.tree); after != before {
		t.Errorf("the tree is a new directory (%d, was %d), which a bind mount would not follow", after, before)
	}
	if _, err := os.Stat(filepath.Join(following.directory, nextTree)); err == nil {
		t.Error("the checkout beside the tree stayed after the swap")
	}
	if _, trouble := following.condition(); trouble != "" {
		t.Errorf("the condition says %q, want nothing", trouble)
	}
}

func TestRefreshLeavesATreeThatIsOnTheRefAlone(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	following := publishedVolume(t, answering, "csi-1", url, map[string]string{"pull": "1h"})

	written := inodeOf(t, filepath.Join(following.tree, "a.txt"))
	followerOf(answering, url).refresh(t.Context(), following)
	if again := inodeOf(t, filepath.Join(following.tree, "a.txt")); again != written {
		t.Error("a fetch that moved nothing rewrote the tree")
	}
}

func TestRefreshReportsAFetchThatFailsAfterOneThatWorked(t *testing.T) {
	logs := &strings.Builder{}
	answering, _ := testNode(t, logs)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	following := publishedVolume(t, answering, "csi-1", url, map[string]string{"pull": "1h"})
	loop := followerOf(answering, url)

	if err := os.RemoveAll(source); err != nil {
		t.Fatalf("removing the forge: %v", err)
	}
	loop.refresh(t.Context(), following)

	commit, trouble := following.condition()
	if trouble == "" {
		t.Error("a failed fetch left the condition normal")
	}
	if commit == "" {
		t.Error("a failed fetch forgot the commit the tree is on")
	}
	if got := readTree(t, following.tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
		t.Errorf("a failed fetch changed the tree to %v", got)
	}
	posted := eventsOf(t, answering)
	if len(posted) != 1 || posted[0].Reason != reasonFailed {
		t.Fatalf("a failed fetch posted %v, want one failure", posted)
	}

	// The Event is posted once, not on every fetch that keeps failing.
	loop.refresh(t.Context(), following)
	if got := len(eventsOf(t, answering)); got != 1 {
		t.Errorf("two failed fetches posted %d events, want 1", got)
	}
	if !strings.Contains(logs.String(), "the fetch failed") {
		t.Errorf("the log is %q, want the failure in it", logs)
	}
}

func TestRefreshClearsTheConditionWhenTheForgeComesBack(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	following := publishedVolume(t, answering, "csi-1", url, map[string]string{"pull": "1h"})
	loop := followerOf(answering, url)

	following.reportTrouble("the forge was not there")
	loop.refresh(t.Context(), following)
	if _, trouble := following.condition(); trouble != "" {
		t.Errorf("the condition says %q after a fetch that worked, want nothing", trouble)
	}
}

func TestRefreshReportsARefThatIsGoneUpstream(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	following := publishedVolume(t, answering, "csi-1", url, map[string]string{"pull": "1h"})

	git(t, source, "checkout", "--quiet", "-b", "other")
	git(t, source, "branch", "--quiet", "--delete", "--force", "main")

	followerOf(answering, url).refresh(t.Context(), following)
	_, trouble := following.condition()
	if trouble == "" {
		t.Fatal("a ref that is gone upstream left the condition normal")
	}
	if !strings.Contains(trouble, "main") {
		t.Errorf("the condition says %q, want the ref named", trouble)
	}
}

func TestRefreshReportsACheckoutItCannotMake(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	following := publishedVolume(t, answering, "csi-1", url, map[string]string{"pull": "1h"})
	commitFiles(t, source, map[string]string{"a.txt": "two"})

	// A file where the checkout goes stops the checkout, and the volume
	// says so.
	writeFiles(t, following.directory, map[string]string{nextTree: ""})
	if err := os.Chmod(following.directory, 0o500); err != nil {
		t.Fatalf("holding the directory closed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(following.directory, 0o700) })

	followerOf(answering, url).refresh(t.Context(), following)
	if _, trouble := following.condition(); trouble == "" {
		t.Error("a checkout that failed left the condition normal")
	}
}

func TestTheLoopFetchesOnItsOwn(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	following := publishedVolume(t, answering, "csi-1", url, map[string]string{"pull": "50ms"})

	want := commitFiles(t, source, map[string]string{"a.txt": "two"})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if commit, _ := following.condition(); commit == want {
			if got := readTree(t, following.tree); sameTree(got, map[string]string{"a.txt": "two"}) {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	commit, trouble := following.condition()
	t.Fatalf("the loop did not reach %s within 30s: it is on %s (%q)", want, commit, trouble)
}

func TestRefreshReportsWhatTheFetchCannotDo(t *testing.T) {
	for _, c := range []struct {
		name  string
		stand func(t *testing.T, following *volume, source string)
	}{
		{
			name: "a key it cannot write",
			stand: func(t *testing.T, following *volume, _ string) {
				following.credentials = &credentials{privateKey: "KEY", username: defaultUsername}
				readOnlyDir(t, following.directory)
			},
		},
		{
			name: "a checkout it cannot make",
			stand: func(t *testing.T, following *volume, source string) {
				commitFiles(t, source, map[string]string{"a.txt": "two"})
				readOnlyDir(t, following.directory)
			},
		},
		{
			name: "a published tree it cannot write",
			stand: func(t *testing.T, following *volume, source string) {
				commitFiles(t, source, map[string]string{"a.txt": "two"})
				readOnlyDir(t, following.tree)
			},
		},
		{
			name: "a ref that names no commit",
			stand: func(t *testing.T, following *volume, source string) {
				following.attributes.ref = blobRef(t, source)
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			url := fileURL(source)
			following := publishedVolume(t, answering, "csi-1", url, map[string]string{"pull": "1h"})
			c.stand(t, following, source)

			followerOf(answering, url).refresh(t.Context(), following)
			if _, trouble := following.condition(); trouble == "" {
				t.Error("the condition is normal after a fetch that could not finish")
			}
		})
	}
}
