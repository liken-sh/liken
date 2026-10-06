package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	kevents "github.com/liken-sh/liken/kubernetes/events"
)

// Each fault reports whether it is the first of its kind, whatever
// fault of another kind stands, so each posts its own Event.
func TestEachFaultIsFirstOfItsKindWhileAnotherStands(t *testing.T) {
	for _, c := range []struct {
		name     string
		standing func(*volume) bool
		next     func(*volume) bool
	}{
		{
			name:     "a push failure after upstream moved",
			standing: func(v *volume) bool { return v.reportUpstreamMoved("upstream moved") },
			next:     func(v *volume) bool { return v.reportPushFailed("the remote refused the push") },
		},
		{
			name:     "a fetch failure after a push failure",
			standing: func(v *volume) bool { return v.reportPushFailed("the remote refused the push") },
			next:     func(v *volume) bool { return v.reportFetchFailed("the forge is not there") },
		},
		{
			name:     "upstream moved after a fetch failure",
			standing: func(v *volume) bool { return v.reportFetchFailed("the forge is not there") },
			next:     func(v *volume) bool { return v.reportUpstreamMoved("upstream moved") },
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			held := volumeNamed("config")
			if !c.standing(held) || c.standing(held) {
				t.Fatal("the standing fault is not first once and then a repeat")
			}
			if !c.next(held) {
				t.Error("the fault of another kind is not the first of its kind")
			}
		})
	}
}

// The defect this guards: a writeable volume whose stage found that
// upstream moved, and whose push then failed, posted no Event for the
// push.
func TestAPushThatFailsAfterUpstreamMovedPostsItsEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, held, remote := pushedVolume(t, io.Discard, nil)
		held.reportUpstreamMoved("upstream moved: main is at 9b1c0de and the tree holds 1 uncommitted paths")
		if err := os.RemoveAll(remote); err != nil {
			t.Fatalf("removing the remote: %v", err)
		}

		answering.push(t.Context(), held)
		failed := eventsWithReason(t, answering, reasonPushFailed)
		if len(failed) != 2 || failed[0].Type != kevents.TypeWarning {
			t.Errorf("the failure posted %+v, want a Warning on the pod and one on the claim", failed)
		}
	})
}

// A stage finds these faults before the driver has found the pod or the
// claim, so the arming posts each one on the claim once it finds it.
func TestAStageFaultIsPostedOnTheClaim(t *testing.T) {
	for _, c := range []struct {
		name   string
		reason string
		says   string
		stage  func(t *testing.T, answering *node, remote string)
	}{
		{
			name:   "upstream moved past uncommitted writes",
			reason: reasonUpstreamMoved,
			says:   "1 uncommitted paths",
			stage: func(t *testing.T, answering *node, remote string) {
				held, request := stagedVolume(t, answering, "config", fileURL(remote))
				writeFiles(t, held.tree, map[string]string{"draft.txt": "unsaved"})
				remoteCommit(t, remote, map[string]string{"b.txt": "two"})
				restaged(t, answering, request)
			},
		},
		{
			name:   "the remote deleted the ref",
			reason: reasonRefDeleted,
			says:   "the remote holds no main",
			stage: func(t *testing.T, answering *node, remote string) {
				held, request := stagedVolume(t, answering, "config", fileURL(remote))
				driverCommit(t, answering, held, map[string]string{"c.txt": "three"})
				git(t, remote, "update-ref", "-d", "refs/heads/main")
				restaged(t, answering, request)
			},
		},
		{
			name:   "another work tree holds unpushed commits",
			reason: reasonAbandoned,
			says:   "the work tree of old holds unpushed commits",
			stage: func(t *testing.T, answering *node, remote string) {
				answering.sweepAfter = time.Hour
				old := unstagedVolume(t, answering, "old", fileURL(remote))
				unstagedAgo(t, answering, "old", 30*time.Hour)
				driverCommit(t, answering, old, map[string]string{"c.txt": "three"})
				answering.sweepStore(t.Context())
				stagedVolume(t, answering, "config", fileURL(remote))
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				remote := bareRemote(t, map[string]string{"a.txt": "one"})
				answering, _ := testNode(t, io.Discard)
				boundVolume(t, answering, "config", "")
				c.stage(t, answering, remote)

				posted := eventsWithReason(t, answering, c.reason)
				if len(posted) != 1 {
					t.Fatalf("the stage posted %+v, want one %s Event", eventsOf(t, answering), c.reason)
				}
				if posted[0].InvolvedObject.Kind != "PersistentVolumeClaim" ||
					posted[0].Type != kevents.TypeWarning || !strings.Contains(posted[0].Message, c.says) {
					t.Errorf("the Event is %+v, want a Warning on the claim that says %q", posted[0], c.says)
				}
			})
		})
	}
}

// The first fetch that works after a failure posts one Event, which
// closes the GitFetchFailed Warning in `kubectl describe`. A fetch that
// works after another that worked posts nothing.
func TestAFetchThatWorksAfterAFailurePostsTheRecovery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		url := fileURL(source)
		following := publishedVolume(t, answering, "csi-1", url, map[string]string{"pull": "1h"})
		loop := followerOf(answering, url)
		following.reportFetchFailed("the forge was not there")

		loop.refresh(t.Context(), following)
		loop.refresh(t.Context(), following)

		recovered := eventsWithReason(t, answering, reasonRecovered)
		if len(recovered) != 1 || recovered[0].Type != kevents.TypeNormal || recovered[0].Count != 1 {
			t.Fatalf("two fetches that worked posted %+v, want one Normal %s", recovered, reasonRecovered)
		}
		if !strings.Contains(recovered[0].Message, "fetched main at") {
			t.Errorf("the Event says %q, want the ref and the commit", recovered[0].Message)
		}
	})
}
