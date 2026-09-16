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
// wait the operator gives it.
func syncingEnricher(t *testing.T, catalog *Catalog) *enricher {
	t.Helper()
	shorterSyncInterval(t)

	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	work.syncTimeout = scanTestTimeout
	return work
}

// Writes the finished scan run a synced copy has to hold, naming the
// write the walking agent made.
func walkLanded(t *testing.T, catalog *Catalog, library string, at time.Time) {
	t.Helper()
	run := libraryRun{Worker: workerScan, Job: "movies-scan-1", Started: at.Add(-time.Minute), Finished: at}
	actor, version, err := catalog.UpsertRun(t.Context(), library, run)
	if err != nil {
		t.Fatal(err)
	}
	run.Actor, run.Version = actor, version
	if _, _, err := catalog.UpsertRun(t.Context(), library, run); err != nil {
		t.Fatal(err)
	}
}

// A walk that changes no count still has to reach the copy before the
// container reads its gap, and the runs row is what says it has.
func TestAContainerWaitsUntilItsCopyHoldsTheWalk(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	seedProbeGap(t, catalog, work.root, "The Thing (1982)", "The Thing (1982).mkv")
	done := make(chan error, 1)
	go func() { done <- work.awaitCatalogSync(t.Context()) }()

	select {
	case err := <-done:
		t.Fatalf("the wait ended on a copy that had not seen the walk: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	walkLanded(t, catalog, work.library, time.Date(2026, 9, 3, 23, 0, 0, 0, time.UTC))

	if err := <-done; err != nil {
		t.Fatalf("the wait failed after the walk's row landed: %v", err)
	}
}

// What the wait reads off the runs table: the row it needs, the
// rows it stands on, and the row from before the confirmation existed,
// which carries no version and is taken as it is.
func TestWhichScanRunEndsTheWait(t *testing.T) {
	cases := []struct {
		name string
		run  libraryRun
		want bool
	}{
		{
			name: "no scan run at all",
			run:  libraryRun{Worker: workerRescan, Job: "movies-rescan-1", Finished: time.Unix(20, 0)},
		},
		{
			name: "a walk that has not finished",
			run:  libraryRun{Worker: workerScan, Job: "movies-scan-1", Started: time.Unix(10, 0)},
		},
		{
			// A walk written before the confirmation existed names no
			// version, and there is nothing about it to prove.
			name: "a finished walk written before this build",
			run:  libraryRun{Worker: workerScan, Job: "movies-scan-1", Finished: time.Unix(20, 0)},
			want: true,
		},
		{
			name: "a walk whose version this copy does not hold",
			run: libraryRun{Worker: workerScan, Job: "movies-scan-1", Finished: time.Unix(20, 0),
				Actor: "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d", Version: 99},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			catalog, _ := newSQLiteCatalog(t)
			if _, _, err := catalog.UpsertRun(t.Context(), "house/movies", testCase.run); err != nil {
				t.Fatal(err)
			}

			synced, err := catalogSynced(t.Context(), catalog, "house/movies")

			if err != nil {
				t.Fatal(err)
			}
			if synced != testCase.want {
				t.Errorf("catalogSynced = %v, want %v", synced, testCase.want)
			}
		})
	}
}

// A walk written before the confirmation existed names no version, so
// it is synced on its own, gaps or not.
func TestAWalkFromBeforeThisBuildIsSyncedWithGapsPresent(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	if _, _, err := catalog.UpsertRun(t.Context(), work.library, libraryRun{
		Worker: workerScan, Job: "movies-scan-1", Finished: time.Unix(20, 0),
	}); err != nil {
		t.Fatal(err)
	}
	agent.recordGap(t, "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d", 1, 110)

	if err := work.awaitCatalogSync(t.Context()); err != nil {
		t.Fatalf("the wait held on a run from before this build: %v", err)
	}
}

// Ranges left by agents that died with versions unsent never fill, and
// they say nothing about the walk.
func TestAnotherWritersHoleDoesNotBlockTheWait(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	walkLanded(t, catalog, work.library, time.Unix(1_700_000_000, 0).UTC())
	agent.recordGap(t, "1a2b3c4d-5e6f-4a7b-8c9d-0e1f2a3b4c5d", 1, 136)

	if err := work.awaitCatalogSync(t.Context()); err != nil {
		t.Fatalf("the wait held on another writer's missing range: %v", err)
	}
}

// A gap of the walk's own writer that reaches back over the version the
// run names leaves the walk unheld.
func TestAHoleInTheWalksOwnWriterBlocksTheWait(t *testing.T) {
	catalog, agent := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	walkLanded(t, catalog, work.library, time.Unix(1_700_000_000, 0).UTC())
	agent.recordGap(t, sqliteAgentActor, 1, 1000)
	work.syncTimeout = 100 * time.Millisecond

	if err := work.awaitCatalogSync(t.Context()); err == nil {
		t.Error("the wait ended on a copy missing the walk writer's versions")
	}
}

func TestAContainerWhoseCopyAlreadyHoldsTheWalkReadsItsGapAtOnce(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	seedProbeGap(t, catalog, work.root, "The Thing (1982)", "The Thing (1982).mkv")
	walkLanded(t, catalog, work.library, time.Unix(1_700_000_000, 0).UTC())

	if err := work.awaitCatalogSync(t.Context()); err != nil {
		t.Fatalf("the wait failed on a copy that already held the walk: %v", err)
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
	catalog, _ := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	folder := "The Thing (1982)"
	seedProbeGap(t, catalog, work.root, folder, "The Thing (1982).mkv")
	walkLanded(t, catalog, work.library, time.Unix(1_700_000_000, 0).UTC())
	answering(t, answeringProbe(ffprobeOfOneFile))

	if err := work.runFacts(t.Context(), []string{factProbe}); err != nil {
		t.Fatalf("the probe container failed: %v", err)
	}

	sidecar := readFileString(t, filepath.Join(work.root, folder, movieSidecarName))
	if !strings.Contains(sidecar, "<codec>h264</codec>") {
		t.Errorf("the sidecar holds no stream details:\n%s", sidecar)
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
	catalog, _ := newSQLiteCatalog(t)
	work := syncingEnricher(t, catalog)
	folder := "The Thing (1982)"
	writeFile(t, filepath.Join(work.root, folder, "thing.mkv"), "video")
	seedIdentityGap(t, catalog, libraryKindMovies, folder, "1982", 0)
	walkLanded(t, catalog, work.library, time.Unix(1_700_000_000, 0).UTC())
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "The Thing", "1982"): `{"results":[` +
			tmdbResultJSON(1091, "The Thing", "1982-06-25") + `]}`,
	})
	answeringTMDb(t, client)

	if err := work.runFacts(t.Context(), []string{factIdentity}); err != nil {
		t.Fatalf("the identity container failed: %v", err)
	}

	sidecar := readFileString(t, filepath.Join(work.root, folder, movieSidecarName))
	if !strings.Contains(sidecar, `<uniqueid type="tmdb" default="true">1091</uniqueid>`) {
		t.Errorf("the sidecar holds no id:\n%s", sidecar)
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
