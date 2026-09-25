package main

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests run one fact container against a catalog loaded with
// the shipped schema, so the wait for the copy runs over real rows and the
// cr-sqlite bookkeeping a real agent holds.

// The poll of the local copy runs in milliseconds here, so a test proves the
// wait in the time a tick takes.
func shorterSyncInterval(t *testing.T) {
	t.Helper()
	was := catalogSyncInterval
	t.Cleanup(func() { catalogSyncInterval = was })
	catalogSyncInterval = 5 * time.Millisecond
}

// SyncingEnricher builds one fact container with the bound on its
// wait the operator gives it, and a target another agent wrote.
func syncingEnricher(t *testing.T, catalog *Catalog) *enricher {
	t.Helper()
	shorterSyncInterval(t)

	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	work.syncTimeout = scanTestTimeout
	work.sync = syncTarget{actor: otherAgent, version: 1}
	return work
}

// Another agent's run, which a copy has to receive before it holds it: the
// target a Job carries, from the run the last Job of the Library confirmed.
const otherAgent = "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d"

// A copy waits until the target's agent reaches its version, and a sync
// from that agent is what ends the wait.
func TestAContainerWaitsUntilItsCopyHoldsTheTarget(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	work.sync = syncTarget{actor: otherAgent, version: 40}
	agent.holdVersion(t, otherAgent, 12)
	done := make(chan error, 1)
	go func() { done <- work.awaitCatalogSync(t.Context()) }()

	select {
	case err := <-done:
		t.Fatalf("the wait ended on a copy that had not reached the target: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	agent.holdVersion(t, otherAgent, 40)

	if err := <-done; err != nil {
		t.Fatalf("the wait failed after the copy reached the target: %v", err)
	}
}

// What the wait reads: no target, a target the copy holds, a version it
// does not hold, and a hole in the target's own agent.
func TestWhichCopyHoldsTheTarget(t *testing.T) {
	cases := []struct {
		name   string
		target syncTarget
		held   int64
		gap    bool
		want   bool
	}{
		{name: "a Library with no confirmed run", want: true},
		{name: "a copy that holds the version", target: syncTarget{actor: otherAgent, version: 40},
			held: 41, want: true},
		{name: "a copy behind the version", target: syncTarget{actor: otherAgent, version: 40}, held: 39},
		{name: "a hole that reaches back over the version", target: syncTarget{actor: otherAgent, version: 40},
			held: 41, gap: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			catalog, agent := newSQLiteCatalog(t)
			agent.holdVersion(t, otherAgent, one.held)
			if one.gap {
				agent.recordGap(t, otherAgent, 1, 30)
			}

			synced, err := catalogSynced(t.Context(), catalog, one.target)

			if err != nil {
				t.Fatal(err)
			}
			if synced != one.want {
				t.Errorf("catalogSynced = %v, want %v", synced, one.want)
			}
		})
	}
}

// Ranges left by agents that died with versions unsent never fill, and
// they say nothing about the target.
func TestAnotherWritersHoleDoesNotBlockTheWait(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	work.sync = syncTarget{actor: otherAgent, version: 40}
	agent.holdVersion(t, otherAgent, 40)
	agent.recordGap(t, "5a6b7c8d-5e6f-4a7b-8c9d-0e1f2a3b4c5d", 1, 136)

	if err := work.awaitCatalogSync(t.Context()); err != nil {
		t.Fatalf("the wait held on another writer's missing range: %v", err)
	}
}

// The target is the newest finished run that names a version, whatever its
// worker, and no target where no run names one.
func TestTheSyncTargetIsTheNewestConfirmedRun(t *testing.T) {
	cases := []struct {
		name string
		runs []libraryRun
		want syncTarget
	}{
		{name: "no runs"},
		{name: "runs with no version", runs: []libraryRun{
			{Worker: workerScan, Finished: time.Unix(20, 0)},
		}},
		{name: "the newest of two", want: syncTarget{actor: otherAgent, version: 90}, runs: []libraryRun{
			{Worker: workerScan, Finished: time.Unix(20, 0), Actor: sqliteAgentActor, Version: 12},
			{Worker: workerEnrich, Finished: time.Unix(40, 0), Actor: otherAgent, Version: 90},
		}},
		{name: "a run in flight", want: syncTarget{actor: sqliteAgentActor, version: 12}, runs: []libraryRun{
			{Worker: workerScan, Finished: time.Unix(20, 0), Actor: sqliteAgentActor, Version: 12},
			{Worker: workerEnrich, Started: time.Unix(40, 0), Actor: otherAgent, Version: 90},
		}},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := syncTargetFor(one.runs); got != one.want {
				t.Errorf("syncTargetFor = %+v, want %+v", got, one.want)
			}
		})
	}
}

// The target reaches a container through two variables, and a version no
// reader can parse is zero.
func TestTheSyncTargetComesOffTheEnvironment(t *testing.T) {
	if got := syncTargetOf(otherAgent, "40"); got != (syncTarget{actor: otherAgent, version: 40}) {
		t.Errorf("syncTargetOf = %+v, want the agent and version 40", got)
	}
	if got := syncTargetOf(otherAgent, "soon"); got != (syncTarget{actor: otherAgent}) {
		t.Errorf("syncTargetOf = %+v, want version zero", got)
	}
}

func TestAContainerThatCannotReachItsAgentFailsTheWait(t *testing.T) {
	work := syncingEnricher(t, NewCatalog("http://127.0.0.1:1", &http.Client{Timeout: time.Second}))

	if err := work.awaitCatalogSync(t.Context()); err == nil {
		t.Error("the wait ended with no read of its own, want the unreachable agent's error")
	}
}

func TestAStoppedContainerEndsItsWait(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	ctx, stop := context.WithCancel(t.Context())
	stop()

	if err := work.awaitCatalogSync(ctx); err == nil {
		t.Error("the wait ended cleanly on a stopped container, want a failure")
	}
}

func TestAFactContainerFailsWhereTheCopyNeverSyncs(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	work.sync = syncTarget{actor: otherAgent, version: 40}
	work.syncTimeout = 100 * time.Millisecond

	if err := work.runFacts(t.Context(), likenFacts); err == nil {
		t.Error("the container read its gap off an unsynced copy")
	}
}

// the probe the probe fact makes, answered for the life of one test.
func answering(t *testing.T, probe mediaProbe) {
	t.Helper()
	was := probeFile
	t.Cleanup(func() { probeFile = was })
	probeFile = probe
}

func TestTheProbeContainerFillsItsGapOnceTheCopyIsSynced(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	folder := "The Thing (1982)"
	seedProbeGap(t, catalog, work.root, folder, "The Thing (1982).mkv")
	agent.holdVersion(t, otherAgent, 1)
	answering(t, answeringProbe(ffprobeOfOneFile))

	if err := work.runFacts(t.Context(), []string{factProbe}); err != nil {
		t.Fatalf("the probe container failed: %v", err)
	}

	nfo := readFileString(t, filepath.Join(work.root, folder, movieNFOName))
	if !strings.Contains(nfo, "<codec>h264</codec>") {
		t.Errorf("the .nfo file holds no stream details:\n%s", nfo)
	}
}

// the provider the identity fact asks, answered by a fake TMDb for the life
// of one test.
func answeringTMDb(t *testing.T, client *tmdbClient) {
	t.Helper()
	was := tmdbAPIBase
	t.Cleanup(func() { tmdbAPIBase = was })
	tmdbAPIBase = client.base
	t.Setenv(tmdbTokenVariable, "a-token")
}

func TestTheIdentityContainerFillsItsGapOnceTheCopyIsSynced(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	folder := "The Thing (1982)"
	writeFile(t, filepath.Join(work.root, folder, "thing.mkv"), "video")
	seedIdentityGap(t, catalog, libraryKindMovies, folder, "1982", 0)
	agent.holdVersion(t, otherAgent, 1)
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "The Thing", "1982"): `{"results":[` +
			tmdbResultJSON(1091, "The Thing", "1982-06-25") + `]}`,
	})
	answeringTMDb(t, client)

	if err := work.runFacts(t.Context(), []string{factIdentity}); err != nil {
		t.Fatalf("the identity container failed: %v", err)
	}

	nfo := readFileString(t, filepath.Join(work.root, folder, movieNFOName))
	if !strings.Contains(nfo, `<uniqueid type="tmdb" default="true">1091</uniqueid>`) {
		t.Errorf("the .nfo file holds no id:\n%s", nfo)
	}
}

func TestTheSyncTimeoutComesOffTheEnvironment(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want time.Duration
	}{
		{name: "the environment names none", raw: "", want: defaultSyncTimeout},
		{name: "the environment names a wait", raw: "90s", want: 90 * time.Second},
		{name: "a value no reader can parse", raw: "soon", want: defaultSyncTimeout},
		{name: "a wait of no time at all", raw: "0s", want: defaultSyncTimeout},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			if got := syncTimeout(one.raw); got != one.want {
				t.Errorf("syncTimeout(%q) = %s, want %s", one.raw, got, one.want)
			}
		})
	}
}
