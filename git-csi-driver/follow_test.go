package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"
)

// publishedVolume publishes an inline volume on the node and returns the
// node's record of it.
func publishedVolume(t *testing.T, n *node, id, url string, extra map[string]string) *volume {
	t.Helper()
	request := publishRequest(t, id, url, extra)
	if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
		t.Fatalf("NodePublishVolume %s: %v", id, err)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.volumes[id]
}

// followerOf returns a follower for the URL whose loop is not running,
// so a test can call its methods directly.
func followerOf(n *node, url string) *follower {
	return &follower{
		node:       n,
		repository: n.store.repository(url),
		wake:       make(chan struct{}, 1),
		volumes:    map[string]*volume{},
	}
}

// inodeOf returns the inode of the path. A bind mount follows its
// directory by inode, so the inode shows whether a directory was
// replaced.
func inodeOf(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return info.Sys().(*syscall.Stat_t).Ino
}

func TestTheIntervalIsTheShortestPullOfTheVolumesThatShare(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)

	loop := followerOf(n, url)
	if got, timed := loop.interval(); timed {
		t.Errorf("a follower of no volumes returned %v, want no timer", got)
	}
	loop.add(&volume{id: "csi-1", attributes: &attributes{pull: pullPolicy{mode: pullOnDemand}}})
	if got, timed := loop.interval(); timed {
		t.Errorf("a follower of a volume that pulls on demand returned %v, want no timer", got)
	}
	loop.add(&volume{id: "csi-2", attributes: &attributes{pull: pullEvery(time.Hour)}})
	if got, timed := loop.interval(); !timed || got != time.Hour {
		t.Errorf("the interval is %v (timed: %v), want %v", got, timed, time.Hour)
	}
	loop.add(&volume{id: "csi-3", attributes: &attributes{pull: pullEvery(time.Minute)}})
	if got, timed := loop.interval(); !timed || got != time.Minute {
		t.Errorf("the interval is %v (timed: %v), want %v", got, timed, time.Minute)
	}
	loop.remove(&volume{id: "csi-3", attributes: &attributes{pull: pullEvery(time.Minute)}})
	if got, timed := loop.interval(); !timed || got != time.Hour {
		t.Errorf("the interval is %v (timed: %v), want %v", got, timed, time.Hour)
	}
}

func TestAVolumeThatPullsOnDemandJoinsTheLoopWithNoTimer(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	demandedVolume(t, n, "franchises", url, "on-demand")

	n.mu.Lock()
	loop, found := n.followers[n.store.repository(url).name]
	n.mu.Unlock()
	if !found {
		t.Fatal("a volume that pulls on demand joined no loop")
	}
	if got, timed := loop.interval(); timed {
		t.Errorf("the loop set a timer of %v, want none", got)
	}
}

func TestOneLoopFollowsARepositoryForEveryVolumeOfIt(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	name := n.store.repository(url).name

	first := publishedVolume(t, n, "csi-1", url, map[string]string{"pull": "1h"})
	second := publishedVolume(t, n, "csi-2", url, map[string]string{"pull": "1h"})

	n.mu.Lock()
	loop, found := n.followers[name]
	n.mu.Unlock()
	if !found {
		t.Fatal("no loop follows the repository")
	}
	if got := len(n.followers); got != 1 {
		t.Errorf("the node has %d loops, want 1", got)
	}
	if got := len(loop.snapshot()); got != 2 {
		t.Errorf("the loop follows %d volumes, want 2", got)
	}

	n.mu.Lock()
	n.leaveFetchLoop(first)
	n.mu.Unlock()
	n.mu.Lock()
	stillThere := len(n.followers)
	n.mu.Unlock()
	if stillThere != 1 {
		t.Errorf("the loop stopped while a volume still followed the repository")
	}

	n.mu.Lock()
	n.leaveFetchLoop(second)
	n.mu.Unlock()
	n.mu.Lock()
	gone := len(n.followers)
	n.mu.Unlock()
	if gone != 0 {
		t.Errorf("the node has %d loops after the last volume went, want 0", gone)
	}
}

func TestAVolumeWithPullNeverJoinsNoLoop(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	pinned := publishedVolume(t, n, "csi-1", fileURL(source), map[string]string{"pull": "never"})

	n.mu.Lock()
	loops := len(n.followers)
	n.leaveFetchLoop(pinned)
	n.mu.Unlock()
	if loops != 0 {
		t.Errorf("a volume that pulls never made %d loops, want 0", loops)
	}
}

func TestUnfollowPassesOverARepositoryWithNoLoop(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	n.mu.Lock()
	defer n.mu.Unlock()
	n.leaveFetchLoop(&volume{id: "csi-9", attributes: &attributes{pull: pullEvery(time.Hour), url: "file:///gone"}})
}

func TestRefreshMovesThePublishedTreeInsideTheDirectoryThePodMounts(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one", "docs/b.txt": "two"})
	url := fileURL(source)
	following := publishedVolume(t, n, "csi-1", url, map[string]string{"pull": "1h"})
	before := inodeOf(t, following.tree)

	commitFiles(t, source, map[string]string{"a.txt": "three", "docs/c.txt": "four"})
	if err := os.Remove(filepath.Join(source, "docs", "b.txt")); err != nil {
		t.Fatalf("removing the file: %v", err)
	}
	commitFiles(t, source, nil)

	loop := followerOf(n, url)
	loop.refresh(t.Context(), following)

	want := map[string]string{"a.txt": "three", "docs/c.txt": "four"}
	if got := readTree(t, following.tree); !sameTree(got, want) {
		t.Errorf("the tree has %v, want %v", got, want)
	}
	if after := inodeOf(t, following.tree); after != before {
		t.Errorf("the tree directory is inode %d, want %d, because a bind mount does not follow a new directory", after, before)
	}
	if _, err := os.Stat(filepath.Join(following.directory, nextTree)); err == nil {
		t.Error("the checkout beside the tree stayed after the swap")
	}
	if _, fault := following.commitAndFault(); fault != "" {
		t.Errorf("the health report is %q, want nothing", fault)
	}
}

func TestRefreshLeavesATreeThatIsOnTheRefAlone(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	following := publishedVolume(t, n, "csi-1", url, map[string]string{"pull": "1h"})

	written := inodeOf(t, filepath.Join(following.tree, "a.txt"))
	followerOf(n, url).refresh(t.Context(), following)
	if again := inodeOf(t, filepath.Join(following.tree, "a.txt")); again != written {
		t.Error("a fetch that moved nothing rewrote the tree")
	}
}

func TestRefreshReportsAFetchThatFailsAfterOneThatWorked(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logs := &strings.Builder{}
		n, _ := testNode(t, logs)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		url := fileURL(source)
		following := publishedVolume(t, n, "csi-1", url, map[string]string{"pull": "1h"})
		loop := followerOf(n, url)

		if err := os.RemoveAll(source); err != nil {
			t.Fatalf("removing the forge: %v", err)
		}
		loop.refresh(t.Context(), following)

		commit, fault := following.commitAndFault()
		if fault == "" {
			t.Error("after a failed fetch the health report is empty, want the failure message")
		}
		if commit == "" {
			t.Error("after a failed fetch the health report has no commit, want the commit the tree is on")
		}
		if got := readTree(t, following.tree); !sameTree(got, map[string]string{"a.txt": "one"}) {
			t.Errorf("after a failed fetch the tree has %v, want it unchanged", got)
		}
		posted := eventsOf(t, n)
		if len(posted) != 1 || posted[0].Reason != reasonFailed {
			t.Fatalf("a failed fetch posted %v, want one failure", posted)
		}

		// Only the first failure posts an Event.
		loop.refresh(t.Context(), following)
		if got := len(eventsOf(t, n)); got != 1 {
			t.Errorf("two failed fetches posted %d events, want 1", got)
		}
		if !strings.Contains(logs.String(), "repository fetch for the volume") {
			t.Errorf("the log is %q, want the repository fetch failure in it", logs)
		}
	})
}

func TestRefreshClearsTheHealthReportWhenTheForgeComesBack(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	following := publishedVolume(t, n, "csi-1", url, map[string]string{"pull": "1h"})
	loop := followerOf(n, url)

	following.reportFetchFailed("the forge was not there")
	loop.refresh(t.Context(), following)
	if _, fault := following.commitAndFault(); fault != "" {
		t.Errorf("the health report is %q after a fetch that worked, want nothing", fault)
	}
}

func TestRefreshReportsARefThatIsGoneUpstream(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	following := publishedVolume(t, n, "csi-1", url, map[string]string{"pull": "1h"})

	git(t, source, "checkout", "--quiet", "-b", "other")
	git(t, source, "branch", "--quiet", "--delete", "--force", "main")

	followerOf(n, url).refresh(t.Context(), following)
	_, fault := following.commitAndFault()
	if fault == "" {
		t.Fatal("after the ref was deleted upstream the health report is empty, want the failure message")
	}
	if !strings.Contains(fault, "main") {
		t.Errorf("the health report is %q, want the ref named", fault)
	}
}

func TestRefreshReportsACheckoutItCannotMake(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
	url := fileURL(source)
	following := publishedVolume(t, n, "csi-1", url, map[string]string{"pull": "1h"})
	commitFiles(t, source, map[string]string{"a.txt": "two"})

	// A file at the checkout path makes the checkout fail, and the
	// volume reports the failure.
	writeFiles(t, following.directory, map[string]string{nextTree: ""})
	if err := os.Chmod(following.directory, 0o500); err != nil {
		t.Fatalf("making the directory read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(following.directory, 0o700) })

	followerOf(n, url).refresh(t.Context(), following)
	if _, fault := following.commitAndFault(); fault == "" {
		t.Error("after a checkout that failed the health report is empty, want the failure message")
	}
}

func TestTheLoopFetchesOnItsIntervalWithoutADemand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		url := fileURL(source)
		following := publishedVolume(t, n, "csi-1", url, map[string]string{"pull": "50ms"})

		want := commitFiles(t, source, map[string]string{"a.txt": "two"})
		time.Sleep(50 * time.Millisecond)
		waitForCommit(t, following, want)
		if got := readTree(t, following.tree); !sameTree(got, map[string]string{"a.txt": "two"}) {
			t.Errorf("the tree has %v, want the commit the loop fetched", got)
		}
	})
}

func TestRefreshReportsWhatTheFetchCannotDo(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup func(t *testing.T, following *volume, source string)
	}{
		{
			name: "a key it cannot write",
			setup: func(t *testing.T, following *volume, _ string) {
				following.credentials = &credentials{privateKey: "KEY", username: defaultUsername}
				readOnlyDir(t, following.directory)
			},
		},
		{
			name: "a checkout it cannot make",
			setup: func(t *testing.T, following *volume, source string) {
				commitFiles(t, source, map[string]string{"a.txt": "two"})
				readOnlyDir(t, following.directory)
			},
		},
		{
			name: "a published tree it cannot write",
			setup: func(t *testing.T, following *volume, source string) {
				commitFiles(t, source, map[string]string{"a.txt": "two"})
				readOnlyDir(t, following.tree)
			},
		},
		{
			name: "a ref that names no commit",
			setup: func(t *testing.T, following *volume, source string) {
				following.attributes.ref = blobRef(t, source)
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			n, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			url := fileURL(source)
			following := publishedVolume(t, n, "csi-1", url, map[string]string{"pull": "1h"})
			c.setup(t, following, source)

			followerOf(n, url).refresh(t.Context(), following)
			if _, fault := following.commitAndFault(); fault == "" {
				t.Error("after a refresh that did not finish the health report is empty, want the failure message")
			}
		})
	}
}
