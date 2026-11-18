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

// resumedVolume returns the node's volume with the volume ID.
func resumedVolume(again *node, id string) *volume {
	again.mu.Lock()
	defer again.mu.Unlock()
	return again.volumes[id]
}

// passEnded takes and releases the repository's lock. A pass holds
// that lock for its whole run, so a test that saw one volume's tree
// change calls passEnded before it reads the other volumes of the pass.
func passEnded(again *node, url string) {
	again.store.repository(url).lock()()
}

func TestAVolumeThatWaitsForItsCredentialFetchesNothingWhenAnotherVolumeOfItsRepositoryFetches(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n, _ := testNode(t, io.Discard)
		source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
		url := fileURL(source)
		requests := map[string]*csi.NodePublishVolumeRequest{}
		for _, id := range []string{"csi-1", "csi-2"} {
			requests[id] = publishRequest(t, id, url, map[string]string{"pull": "1h"})
			requests[id].Secrets = secretA
			if _, err := n.NodePublishVolume(t.Context(), requests[id]); err != nil {
				t.Fatalf("publishing %s: %v", id, err)
			}
		}
		commitFiles(t, source, map[string]string{"b.txt": "two"})

		again := restartedQuiet(t, n)
		returned, waiting := resumedVolume(again, "csi-1"), resumedVolume(again, "csi-2")
		if _, err := again.NodePublishVolume(t.Context(), requests["csi-1"]); err != nil {
			t.Fatalf("republishing csi-1: %v", err)
		}
		waitForFile(t, returned.tree, "b.txt")
		passEnded(again, url)

		if _, err := os.Stat(filepath.Join(waiting.tree, "b.txt")); err == nil {
			t.Error("the volume that waits for its credential changed state when the other volume fetched")
		}
		if _, message := waiting.health(); !strings.Contains(message, "no credential") {
			t.Errorf("the waiting volume reports %q, want the credential it waits for", message)
		}
		if _, fault := waiting.commitAndFault(); fault != "" {
			t.Errorf("the waiting volume records the failure %q, want no fetch at all", fault)
		}

		if _, err := again.NodePublishVolume(t.Context(), requests["csi-2"]); err != nil {
			t.Fatalf("republishing csi-2: %v", err)
		}
		waitForFile(t, waiting.tree, "b.txt")
	})
}

func TestADemandForAVolumeThatWaitsForItsCredentialWakesNoPass(t *testing.T) {
	n, _ := testNode(t, io.Discard)
	loop := followerOf(n, "file:///nowhere")
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
		n, _ := testNode(t, io.Discard)
		remote := bareRemote(t, map[string]string{"a.txt": "one"})
		boundVolume(t, n, "config", "config-eager")
		createClass(t, n, "config-eager", nil)
		staged := stageRequest(t, "config", fileURL(remote), nil)
		staged.Secrets = secretA
		if _, err := n.NodeStageVolume(t.Context(), staged); err != nil {
			t.Fatalf("staging the volume: %v", err)
		}
		request := persistentPublish(t, staged)
		request.Secrets = secretA
		if _, err := n.NodePublishVolume(t.Context(), request); err != nil {
			t.Fatalf("publishing the volume: %v", err)
		}
		vol := resumedVolume(n, "config")
		waitForArmed(t, vol, true)
		unwatched(t, n, vol)
		writeFiles(t, vol.tree, map[string]string{"one.yaml": "1"})
		n.commit(t.Context(), vol, vol.policyNow())
		before := strings.TrimSpace(git(t, remote, "rev-parse", "main"))

		again := restartedQuiet(t, n)
		resumed := resumedVolume(again, "config")
		waitForArmed(t, resumed, true)
		again.pushIfDue(t.Context(), resumed, resumed.policyNow(), time.Hour)
		if _, fault := resumed.commitAndFault(); fault != "" {
			t.Errorf("a timed push records the failure %q, want no push at all", fault)
		}
		// The unpublish push is the last push before the pod is gone, so
		// its failure posts an Event.
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
			n, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			placed := strings.TrimSpace(git(t, source, "rev-parse", "HEAD"))
			request, _ := publishedOnce(t, n, c.kind, fileURL(source), c.secrets, c.secrets)

			again := restartedQuiet(t, n)
			resumed := resumedVolume(again, request.VolumeId)
			if _, err := again.NodePublishVolume(t.Context(), request); err != nil {
				t.Fatalf("republishing the volume: %v", err)
			}
			if _, message := resumed.health(); message != "main at "+short(placed) {
				t.Errorf("the resumed volume reports %q, want %q", message, "main at "+short(placed))
			}
		})
	}
}

func TestAPlacementThatCannotRecordItsCommitFails(t *testing.T) {
	for _, blocked := range []string{placedFile + ".next", placedFile} {
		t.Run(blocked, func(t *testing.T) {
			n, _ := testNode(t, io.Discard)
			source := repositoryWithACommit(t, map[string]string{"a.txt": "one"})
			request, vol := publishedOnce(t, n, inlineVolume, fileURL(source), nil, nil)
			commit := commitFiles(t, source, map[string]string{"b.txt": "two"})
			repo := n.store.repository(request.VolumeContext["url"])
			if err := repo.fetch(t.Context(), nil, "main", 0); err != nil {
				t.Fatalf("fetching the volume: %v", err)
			}
			// A directory that contains a file cannot be written as a
			// file or replaced by a rename.
			if err := os.Remove(filepath.Join(vol.directory, placedFile)); err != nil {
				t.Fatalf("removing the recorded commit: %v", err)
			}
			writeFiles(t, filepath.Join(vol.directory, blocked), map[string]string{"x": "x"})

			if err := repo.place(t.Context(), commit, vol.directory, vol.tree); err == nil {
				t.Errorf("place returned no error with %s blocked, want one", blocked)
			}
		})
	}
}
