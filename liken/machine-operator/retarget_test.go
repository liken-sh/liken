package main

// These tests cover a new version target while a release is already
// staged. A staged record of a different release is withdrawn before
// the download starts. A staged record of the target itself is kept.

import (
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/liken/machine"
)

// stagedStore returns a release store, and its root directory, with a
// record of the given release staged for slot B.
func stagedStore(t *testing.T, version, digest string) (machine.ManifestStore, string) {
	t.Helper()
	root := t.TempDir()
	store := machine.SystemReleases(root)
	raw, _, err := machine.RenderSystemRelease(version, "B", digest)
	if err != nil {
		t.Fatalf("rendering the SystemRelease of %s: %v", version, err)
	}
	if err := store.WriteStaged(raw); err != nil {
		t.Fatalf("writing the staged record of %s: %v", version, err)
	}
	return store, root
}

// refusingFetcher returns a fetcher whose release server responds 404 to
// every request, and the count of requests the server received. No
// download completes, so a test sees the staged record as it was when
// the download started.
func refusingFetcher(t *testing.T) (*fetcher, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	return fetcherFor(server), &requests
}

func TestANewTargetKeepsOnlyAStagedRecordOfItself(t *testing.T) {
	cases := []struct {
		name         string
		stagedDigest string
		staged       bool
	}{
		{name: "the staged release is the target", stagedDigest: "sha256:" + strings.Repeat("bb", 32), staged: true},
		{name: "the target republished under a new digest", stagedDigest: "sha256:" + strings.Repeat("cc", 32)},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store, _ := stagedStore(t, "0.2.0", one.stagedDigest)
				f, _ := refusingFetcher(t)

				convergeSystemRelease(store, clusterWithTarget("0.2.0"), autoMachine(), slotBackedFacts("0.1.0", "A"), f, turnAwaiting, nil)
				synctest.Wait()

				if staged, _ := store.LoadStaged(); (staged != nil) != one.staged {
					t.Errorf("staged = %v, want %v", staged != nil, one.staged)
				}
			})
		})
	}
}

func TestANewTargetWithdrawsAnOlderStagedRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, _ := stagedStore(t, "0.1.5", "sha256:"+strings.Repeat("dd", 32))
		f, requests := refusingFetcher(t)

		conv := convergeSystemRelease(store, clusterWithTarget("0.2.0"), autoMachine(), slotBackedFacts("0.1.0", "A"), f, turnAwaiting, nil)
		synctest.Wait()

		if staged, _ := store.LoadStaged(); staged != nil {
			t.Error("the record of release 0.1.5 is still staged, want it withdrawn before 0.2.0 downloads onto its slot")
		}
		if conv.condition.Reason != "Downloading" || requests.Load() == 0 {
			t.Errorf("%s after %d requests, want the download of 0.2.0 started", conv.condition.Reason, requests.Load())
		}
	})
}

// When the staged record cannot be withdrawn, the download must not
// start, because it would write into the slot that the record boots.
func TestAStagedRecordThatCannotBeWithdrawnStopsTheDownload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, root := stagedStore(t, "0.1.5", "sha256:"+strings.Repeat("dd", 32))
		f, requests := refusingFetcher(t)
		readOnly(t, filepath.Join(root, "system"))

		conv := convergeSystemRelease(store, clusterWithTarget("0.2.0"), autoMachine(), slotBackedFacts("0.1.0", "A"), f, turnAwaiting, nil)
		synctest.Wait()

		if conv.condition.Reason != "StagingFailed" || !strings.Contains(conv.condition.Message, "cannot be withdrawn") {
			t.Errorf("condition = %s: %s, want the failed withdrawal", conv.condition.Reason, conv.condition.Message)
		}
		if requests.Load() != 0 {
			t.Errorf("%d requests, want none while the old record is staged", requests.Load())
		}
	})
}

// After a failed download, the pass wakes at the retry time. Each pass
// writes the same retry time into the condition message, so a repeated
// pass does not change the status.
func TestAFailedDownloadSetsTheWakeTimeToItsRetryTime(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, _ := stagedStore(t, "0.1.5", "sha256:"+strings.Repeat("dd", 32))
		f, _ := refusingFetcher(t)
		converge := func(out *passOutcome) convergence {
			return convergeSystemRelease(store, clusterWithTarget("0.2.0"), autoMachine(), slotBackedFacts("0.1.0", "A"), f, turnAwaiting, out)
		}
		converge(nil)
		failed := awaitSettled(f)

		out := &passOutcome{}
		first, second := converge(out), converge(nil)

		retryAt := failed.retryAt.UTC().Format(time.RFC3339)
		if !out.wake.Equal(failed.retryAt) || !strings.Contains(first.condition.Message, retryAt) || first.condition != second.condition {
			t.Errorf("wake %s, conditions %+v and %+v; want a wake at %s and one unchanged message containing it",
				out.wake, first.condition, second.condition, retryAt)
		}
	})
}
