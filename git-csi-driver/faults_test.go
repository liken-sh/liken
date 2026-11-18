package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"testing/synctest"

	kevents "github.com/liken-sh/liken/kubernetes/events"
)

func TestAFaultIsFirstOfItsKindWhileAFaultOfAnotherKindIsRecorded(t *testing.T) {
	for _, c := range []struct {
		name     string
		recorded func(*volume) bool
		next     func(*volume) bool
	}{
		{
			name:     "a push failure after upstream moved",
			recorded: func(v *volume) bool { return v.reportUpstreamMoved("upstream moved") },
			next:     func(v *volume) bool { return v.reportPushFailed("the remote refused the push") },
		},
		{
			name:     "a fetch failure after a push failure",
			recorded: func(v *volume) bool { return v.reportPushFailed("the remote refused the push") },
			next:     func(v *volume) bool { return v.reportFetchFailed("the forge is not there") },
		},
		{
			name:     "upstream moved after a fetch failure",
			recorded: func(v *volume) bool { return v.reportFetchFailed("the forge is not there") },
			next:     func(v *volume) bool { return v.reportUpstreamMoved("upstream moved") },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			vol := volumeNamed("config")
			if !c.recorded(vol) || c.recorded(vol) {
				t.Fatal("the same fault twice in a row was not reported as first and then repeated")
			}
			if !c.next(vol) {
				t.Error("a fault of another kind was reported as a repeat, want first of its kind")
			}
		})
	}
}

func TestAPushThatFailsAfterUpstreamMovedPostsItsEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, vol, remote := committedVolume(t, io.Discard, nil)
		vol.reportUpstreamMoved("upstream moved: main is at 9b1c0de and the tree has 1 uncommitted paths")
		if err := os.RemoveAll(remote); err != nil {
			t.Fatalf("removing the remote: %v", err)
		}

		n.push(t.Context(), vol)
		failed := eventsWithReason(t, n, reasonPushFailed)
		if len(failed) != 2 || failed[0].Type != kevents.TypeWarning {
			t.Errorf("the failure posted %+v, want a Warning on the pod and one on the claim", failed)
		}
	})
}

func TestAStageFaultIsPostedOnTheClaim(t *testing.T) {
	for _, c := range []struct {
		name        string
		reason      string
		wantMessage string
		stage       func(t *testing.T, n *node, remote string)
	}{
		{
			name:        "upstream moved past uncommitted writes",
			reason:      reasonUpstreamMoved,
			wantMessage: "1 uncommitted paths",
			stage: func(t *testing.T, n *node, remote string) {
				vol, request := stagedVolume(t, n, "config", fileURL(remote))
				writeFiles(t, vol.tree, map[string]string{"draft.txt": "unsaved"})
				remoteCommit(t, remote, map[string]string{"b.txt": "two"})
				restaged(t, n, request)
			},
		},
		{
			name:        "the remote deleted the ref",
			reason:      reasonRefDeleted,
			wantMessage: "The remote has no ref main",
			stage: func(t *testing.T, n *node, remote string) {
				vol, request := stagedVolume(t, n, "config", fileURL(remote))
				driverCommit(t, n, vol, map[string]string{"c.txt": "three"})
				git(t, remote, "update-ref", "-d", "refs/heads/main")
				restaged(t, n, request)
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				remote := bareRemote(t, map[string]string{"a.txt": "one"})
				n, _ := testNode(t, io.Discard)
				boundVolume(t, n, "config", "")
				c.stage(t, n, remote)

				posted := eventsWithReason(t, n, c.reason)
				if len(posted) != 1 {
					t.Fatalf("the stage posted %+v, want one %s Event", eventsOf(t, n), c.reason)
				}
				if posted[0].InvolvedObject.Kind != "PersistentVolumeClaim" ||
					posted[0].Type != kevents.TypeWarning || !strings.Contains(posted[0].Message, c.wantMessage) {
					t.Errorf("the Event is %+v, want a Warning on the claim with the message %q", posted[0], c.wantMessage)
				}
			})
		})
	}
}

// The recovery Event follows the GitFetchFailed Warning in `kubectl
// describe`, so a reader sees that the failure ended.
func TestAFetchThatWorksAfterAFailurePostsTheRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		url := fileURL(source)
		following := publishedVolume(t, n, "csi-1", url, map[string]string{"pull": "1h"})
		loop := followerOf(n, url)
		following.reportFetchFailed("the forge was not there")

		loop.refresh(t.Context(), following)
		loop.refresh(t.Context(), following)

		recovered := eventsWithReason(t, n, reasonRecovered)
		if len(recovered) != 1 || recovered[0].Type != kevents.TypeNormal || recovered[0].Count != 1 {
			t.Fatalf("two fetches that worked posted %+v, want one Normal %s", recovered, reasonRecovered)
		}
		if !strings.Contains(recovered[0].Message, "Fetched main at") {
			t.Errorf("the Event message is %q, want the ref and the commit", recovered[0].Message)
		}
	})
}
