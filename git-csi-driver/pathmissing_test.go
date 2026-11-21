package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
)

func TestAStageRefusesACommitThatLacksAnEntry(t *testing.T) {
	for _, c := range []struct {
		name   string
		refuse func(t *testing.T, n *node, url string) error
		// kind is the kind of the object that the refusal's Event is on.
		kind string
	}{
		{
			name: "a read-only claim",
			refuse: func(t *testing.T, n *node, url string) error {
				claimedVolume(t, n, "franchises")
				_, err := n.NodeStageVolume(t.Context(), readOnlyStage(t, "franchises", url,
					map[string]string{"pull": "never", "paths": "a,gone"}))
				return err
			},
			kind: "PersistentVolumeClaim",
		},
		{
			name: "an inline volume",
			refuse: func(t *testing.T, n *node, url string) error {
				_, err := n.NodePublishVolume(t.Context(), publishRequest(t, "franchises", url,
					map[string]string{"pull": "never", "paths": "a,gone"}))
				return err
			},
			kind: "Pod",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n, _ := testNode(t, io.Discard)
				source := repositoryWithACommit(t, map[string]string{"a/x": "one"})
				commit := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))

				err := c.refuse(t, n, fileURL(source))
				want := `paths: commit ` + short(commit) + ` of main has no "gone"`
				if status.Code(err) != codes.FailedPrecondition || status.Convert(err).Message() != want {
					t.Fatalf("the stage returned %v, want FailedPrecondition with %q", err, want)
				}
				refused := eventsWithReason(t, n, reasonRefused)
				if len(refused) != 1 || refused[0].InvolvedObject.Kind != c.kind || refused[0].Message != want {
					t.Errorf("the refusal posted %v, want one Event on the %s with %q", refused, c.kind, want)
				}
				if _, err := os.Stat(n.store.volumeDir("franchises")); err == nil {
					t.Error("a refused stage left the volume's directory behind")
				}
			})
		})
	}
}

func TestAStaleStageNamesTheFailedFetchAndTheMissingEntry(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	source := repositoryWithACommit(t, map[string]string{"a/x": "one"})
	url := fileURL(source)
	copied := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))
	publishedVolume(t, n, "csi-1", url, map[string]string{"pull": "never"})
	// The remote gets the entry, and then the node cannot reach it, so
	// the node's copy of main lacks the entry.
	commitFiles(t, source, map[string]string{"new/x": "new"})
	if err := os.RemoveAll(source); err != nil {
		t.Fatalf("removing the remote: %v", err)
	}

	_, err := n.NodePublishVolume(t.Context(), publishRequest(t, "csi-2", url,
		map[string]string{"pull": "never", "paths": "new", "offline": "allowStale"}))
	want := `paths: commit ` + short(copied) + ` of main has no "new", and the node used its copy of main because the fetch failed: git fetch`
	if status.Code(err) != codes.FailedPrecondition || !strings.HasPrefix(status.Convert(err).Message(), want) {
		t.Errorf("the stage returned %v, want FailedPrecondition that starts with %q", err, want)
	}
}

// missingVolume stages a read-only claim with the entry a and publishes
// it to one pod. Then the source renames a to c, so the commit of main
// lacks the entry. It returns the volume, a follower of its repository
// whose loop is not running, the source, and the commit that the tree
// is on.
func missingVolume(t *testing.T, n *node) (*volume, *follower, string, string) {
	t.Helper()
	source := repositoryWithACommit(t, map[string]string{"a/x": "one", "b/y": "one"})
	claimedVolume(t, n, "franchises")
	staged, vol := stagedReadOnly(t, n, "franchises", fileURL(source),
		map[string]string{"pull": "1h", "paths": "a"})
	publishedTo(t, n, staged, "reader")
	placed := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))
	git(t, source, "mv", "a", "c")
	commitFiles(t, source, nil)
	loop := followerOf(n, fileURL(source))
	loop.volumes[vol.id] = vol
	return vol, loop, source, placed
}

func TestARefreshKeepsTheTreeOnACommitThatLacksAnEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		vol, loop, source, placed := missingVolume(t, n)
		lacking := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))

		if !loop.refresh(t.Context(), vol) {
			t.Error("refresh returned false, want true, because a fetch again cannot add the entry to the commit")
		}
		if got := readTree(t, vol.tree); !sameTree(got, map[string]string{"a/x": "one"}) {
			t.Errorf("the tree has %v, want the files of %s", got, short(placed))
		}
		want := `Commit ` + short(lacking) + ` of main has no "a", so the tree stays at ` + short(placed)
		if commit, fault := vol.commitAndFault(); commit != placed || fault != want {
			t.Errorf("the volume is at %s with the fault %q, want %s with %q", short(commit), fault, short(placed), want)
		}
		if abnormal, _ := abnormalOf(t, n.metrics, "home", "franchises"); abnormal != 1 {
			t.Errorf("git_csi_volume_abnormal reads %v, want 1", abnormal)
		}
		missing := eventsWithReason(t, n, reasonPathMissing)
		if len(missing) != 2 || missing[0].Type != corev1.EventTypeWarning || missing[0].Message != want {
			t.Errorf("the refresh posted %v, want one Warning on the pod and one on the claim with %q", missing, want)
		}
	})
}

func TestALaterCommitThatAlsoLacksTheEntryPostsNoSecondEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		vol, loop, source, _ := missingVolume(t, n)
		loop.refresh(t.Context(), vol)

		later := commitFiles(t, source, map[string]string{"b/y": "two"})
		loop.refresh(t.Context(), vol)
		loop.refresh(t.Context(), vol)
		if got := len(eventsWithReason(t, n, reasonPathMissing)); got != 2 {
			t.Errorf("three refreshes posted %d GitVolumePathMissing Events, want 2, one on the pod and one on the claim", got)
		}
		if _, fault := vol.commitAndFault(); !strings.HasPrefix(fault, "Commit "+short(later)+" ") {
			t.Errorf("the fault is %q, want it to name the newer commit %s", fault, short(later))
		}
	})
}

func TestACommitWithEveryEntryClearsTheFault(t *testing.T) {
	for _, c := range []struct {
		name string
		move func(t *testing.T, source, placed string)
		want map[string]string
	}{
		{
			name: "a new commit has the entry again",
			move: func(t *testing.T, source, _ string) {
				commitFiles(t, source, map[string]string{"a/x": "three"})
			},
			want: map[string]string{"a/x": "three"},
		},
		{
			name: "the ref moves back to the tree's commit",
			move: func(t *testing.T, source, placed string) {
				git(t, source, "reset", "--quiet", "--hard", placed)
			},
			want: map[string]string{"a/x": "one"},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				n, _ := testNode(t, io.Discard)
				vol, loop, source, placed := missingVolume(t, n)
				loop.refresh(t.Context(), vol)

				c.move(t, source, placed)
				loop.refresh(t.Context(), vol)
				if got := readTree(t, vol.tree); !sameTree(got, c.want) {
					t.Errorf("the tree has %v, want %v", got, c.want)
				}
				if _, fault := vol.commitAndFault(); fault != "" {
					t.Errorf("the fault is %q, want none", fault)
				}
				found := eventsWithReason(t, n, reasonPathFound)
				if len(found) != 2 || found[0].Type != corev1.EventTypeNormal {
					t.Errorf("the refresh posted %v, want one Normal Event on the pod and one on the claim", found)
				}
			})
		})
	}
}

func TestACommitThatLacksAnEntrySatisfiesTheDemand(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		vol, loop, _, _ := missingVolume(t, n)
		// The stage satisfied every demand stamped before it, so the
		// demand comes later.
		time.Sleep(10 * time.Minute)
		demanded := time.Now().Add(-2 * demandSkew)
		loop.wanted = map[string]*volume{vol.id: vol}

		if retry := loop.runPass(t.Context()); retry {
			t.Error("the pass asked for a retry, want none until the ref moves")
		}
		if vol.acceptDemand(demanded, time.Now()) {
			t.Error("the volume accepted a demand from before the pass, want it satisfied by the fetch")
		}
	})
}
