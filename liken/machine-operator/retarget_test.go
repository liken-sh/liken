package main

// What a new version target does to a release that is already staged:
// the record of another release goes before a byte of the new one
// downloads, and the record of the same release stays.

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/apiservertest"
	"github.com/liken-sh/liken/liken/machine"
)

// stagedStore is a system release store with a release staged for
// slot B, under the root it answers.
func stagedStore(t *testing.T, version, digest string) (machine.ManifestStore, string) {
	t.Helper()
	root := t.TempDir()
	store := machine.SystemReleases(root)
	raw, _, err := machine.RenderSystemRelease(version, "B", digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteStaged(raw); err != nil {
		t.Fatal(err)
	}
	return store, root
}

// A fetcher whose channel answers nothing, and the count of what it
// asked. The test sees the order of the staged record and the first
// request, and no download writes a slot.
func refusingFetcher(t *testing.T) (*fetcher, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	server := apiservertest.Start(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	return fetcherFor(server), &requests
}

func TestANewTargetWithdrawsTheStagedReleaseOfAnother(t *testing.T) {
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

				convergeSystemRelease(store, clusterWithTarget("0.2.0"), autoMachine(), slotBackedFacts("0.1.0", "A"), f, turnAwaiting)
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

		conv := convergeSystemRelease(store, clusterWithTarget("0.2.0"), autoMachine(), slotBackedFacts("0.1.0", "A"), f, turnAwaiting)
		synctest.Wait()

		if staged, _ := store.LoadStaged(); staged != nil {
			t.Error("the record of release 0.1.5 still stands while 0.2.0 downloads onto its slot")
		}
		if conv.condition.Reason != "Downloading" || requests.Load() == 0 {
			t.Errorf("%s after %d requests, want the download of 0.2.0 started", conv.condition.Reason, requests.Load())
		}
	})
}

// A record that cannot be withdrawn stops the download, so the slot
// keeps the release that the record names.
func TestARecordThatStaysStopsTheDownload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store, root := stagedStore(t, "0.1.5", "sha256:"+strings.Repeat("dd", 32))
		f, requests := refusingFetcher(t)
		readOnly(t, filepath.Join(root, "system"))

		conv := convergeSystemRelease(store, clusterWithTarget("0.2.0"), autoMachine(), slotBackedFacts("0.1.0", "A"), f, turnAwaiting)
		synctest.Wait()

		if conv.condition.Reason != "StagingFailed" || !strings.Contains(conv.condition.Message, "could not be withdrawn") {
			t.Errorf("condition = %s: %s, want the failed withdrawal", conv.condition.Reason, conv.condition.Message)
		}
		if requests.Load() != 0 {
			t.Errorf("%d requests, want none while the old record stands", requests.Load())
		}
	})
}

// readOnly takes write permission away from a directory until the
// test ends.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
}
