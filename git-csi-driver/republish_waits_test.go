package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
)

// resumedVolume is what the restarted node holds for the handle.
func resumedVolume(again *node, id string) *volume {
	again.mu.Lock()
	defer again.mu.Unlock()
	return again.volumes[id]
}

// passEnded waits until no pass holds the repository's lock. A pass
// takes the lock for its whole run, so a test that saw one volume's
// tree move reads the others only after the pass that moved it ends.
func passEnded(again *node, url string) {
	again.store.repository(url).lock()()
}

func TestAVolumeThatWaitsForItsCredentialFetchesNothingWhenAnotherVolumeOfItsRepositoryFetches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		url := fileURL(source)
		requests := map[string]*csi.NodePublishVolumeRequest{}
		for _, id := range []string{"csi-1", "csi-2"} {
			requests[id] = publishRequest(t, id, url, map[string]string{"pull": "1h"})
			requests[id].Secrets = secretA
			if _, err := answering.NodePublishVolume(t.Context(), requests[id]); err != nil {
				t.Fatalf("NodePublishVolume %s: %v", id, err)
			}
		}
		commitFiles(t, source, map[string]string{"b.txt": "two"})

		again := restartedQuiet(t, answering)
		returned, waiting := resumedVolume(again, "csi-1"), resumedVolume(again, "csi-2")
		if _, err := again.NodePublishVolume(t.Context(), requests["csi-1"]); err != nil {
			t.Fatalf("the republish of csi-1: %v", err)
		}
		waitForFile(t, returned.tree, "b.txt")
		passEnded(again, url)

		if _, err := os.Stat(filepath.Join(waiting.tree, "b.txt")); err == nil {
			t.Error("the volume that waits for its credential moved with the other volume's fetch")
		}
		if _, message := waiting.report(); !strings.Contains(message, "no credential") {
			t.Errorf("the waiting volume reports %q, want the credential it waits for", message)
		}
		if _, trouble := waiting.condition(); trouble != "" {
			t.Errorf("the waiting volume records the failure %q, want no fetch at all", trouble)
		}

		if _, err := again.NodePublishVolume(t.Context(), requests["csi-2"]); err != nil {
			t.Fatalf("the republish of csi-2: %v", err)
		}
		waitForFile(t, waiting.tree, "b.txt")
	})
}

func TestADemandForAVolumeThatWaitsForItsCredentialWakesNoPass(t *testing.T) {
	answering, _ := testNode(t, io.Discard)
	loop := followerOf(answering, "file:///nowhere")
	loop.demanded = make(chan struct{}, 1)
	loop.wanted = map[string]*volume{}
	waiting := &volume{id: "csi-1", credentialLost: true}
	loop.volumes[waiting.id] = waiting

	loop.demand(waiting)
	if len(loop.wanted) != 0 || len(loop.demanded) != 0 {
		t.Errorf("a demand for a waiting volume left %d wanted and %d wakes, want none",
			len(loop.wanted), len(loop.demanded))
	}
}

func TestAWriteableVolumeThatWaitsForItsCredentialPushesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		answering, _ := testNode(t, io.Discard)
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		boundVolume(t, answering, "config", "config-eager")
		armingClass(t, answering, "config-eager", nil)
		staged := stageRequest(t, "config", fileURL(remote), nil)
		staged.Secrets = secretA
		if _, err := answering.NodeStageVolume(t.Context(), staged); err != nil {
			t.Fatalf("NodeStageVolume: %v", err)
		}
		request := persistentPublish(t, staged)
		request.Secrets = secretA
		if _, err := answering.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("NodePublishVolume: %v", err)
		}
		held := resumedVolume(answering, "config")
		waitForArmed(t, held, true)
		unwatched(t, answering, held)
		writeFiles(t, held.tree, map[string]string{"one.yaml": "1"})
		answering.commit(t.Context(), held, held.policyNow())
		before := strings.TrimSpace(git(t, remote, "rev-parse", "main"))

		again := restartedQuiet(t, answering)
		resumed := resumedVolume(again, "config")
		waitForArmed(t, resumed, true)
		again.pushIfDue(t.Context(), resumed, resumed.policyNow(), time.Hour)
		if _, trouble := resumed.condition(); trouble != "" {
			t.Errorf("a timed push records the failure %q, want no push at all", trouble)
		}
		// The unpublish push is the last one the pod gets, so its failure
		// is posted.
		again.push(t.Context(), resumed)
		if len(eventsWithReason(t, again, reasonPushFailed)) == 0 {
			t.Errorf("the unpublish push posted no %s Event", reasonPushFailed)
		}

		if got := strings.TrimSpace(git(t, remote, "rev-parse", "main")); got != before {
			t.Errorf("the remote moved to %s with no credential, want it at %s", got, before)
		}
	})
}

func TestAResumedReadOnlyVolumeReportsTheCommitItsTreeHolds(t *testing.T) {
	for _, c := range []struct {
		name    string
		kind    volumeKind
		secrets map[string]string
	}{
		{name: "an inline volume of a private repository", kind: inlineVolume, secrets: secretA},
		{name: "an inline volume of a public repository", kind: inlineVolume, secrets: nil},
		{name: "a read-only claim of a private repository", kind: readOnlyClaim, secrets: secretA},
	} {
		t.Run(c.name, func(t *testing.T) {
			answering, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			placed := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))
			request, _ := publishedOnce(t, answering, c.kind, fileURL(source), c.secrets, c.secrets)

			again := restartedQuiet(t, answering)
			resumed := resumedVolume(again, request.VolumeId)
			if _, err := again.NodePublishVolume(t.Context(), request); err != nil {
				t.Fatalf("the republish: %v", err)
			}
			if _, message := resumed.report(); message != "main at "+short(placed) {
				t.Errorf("the resumed volume reports %q, want %q", message, "main at "+short(placed))
			}
		})
	}
}

func TestAPlacementThatCannotRecordItsCommitFails(t *testing.T) {
	for _, blocked := range []string{placedFile + ".next", placedFile} {
		t.Run(blocked, func(t *testing.T) {
			answering, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			request, held := publishedOnce(t, answering, inlineVolume, fileURL(source), nil, nil)
			commit := commitFiles(t, source, map[string]string{"b.txt": "two"})
			repo := answering.store.repository(request.VolumeContext["url"])
			if err := repo.fetch(t.Context(), nil, "main", 0); err != nil {
				t.Fatalf("fetching: %v", err)
			}
			// A directory that holds a file can be neither written as a
			// file nor replaced by a rename.
			if err := os.Remove(filepath.Join(held.directory, placedFile)); err != nil {
				t.Fatalf("removing the recorded commit: %v", err)
			}
			writeFiles(t, filepath.Join(held.directory, blocked), map[string]string{"x": "x"})

			if err := repo.place(t.Context(), commit, held.directory, held.tree); err == nil {
				t.Errorf("place answered no error with %s blocked", blocked)
			}
		})
	}
}
