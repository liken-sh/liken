package main

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests run one concern container against the test broker and a real
// catalog, so the wait for the copy runs over a real connection and real
// rows.

// The poll of the local copy runs in milliseconds here, so a test proves the
// wait in the time a tick takes.
func shorterSyncInterval(t *testing.T) {
	t.Helper()
	was := catalogSyncInterval
	t.Cleanup(func() { catalogSyncInterval = was })
	catalogSyncInterval = 5 * time.Millisecond
}

// syncingEnricher builds one concern container with the environment the
// operator gives it: the broker it subscribes on, the Library's status topic,
// and the bound on the wait.
func syncingEnricher(t *testing.T, catalog *Catalog) (*enricher, <-chan *fakeBroker) {
	t.Helper()
	address, accepted := testBroker(t)
	shorterBackoff(t)
	shorterSyncInterval(t)
	t.Setenv(busAddressVariable, address)

	work, _ := testEnricher(t, libraryKindMovies, t.TempDir(), catalog)
	work.statusTopic = libraryStatusTopic(defaultTopicBase, "house", "movies")
	work.syncTimeout = scanTestTimeout
	return work, accepted
}

// The report the standing pod publishes retained, in the two counts the
// container compares its own copy against.
func syncReport(t *testing.T, items, files int) []byte {
	t.Helper()
	payload, err := json.Marshal(libraryReport{Items: items, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

// reportTheCounts answers the container's subscription with the report the
// standing pod holds for the library.
func reportTheCounts(t *testing.T, accepted <-chan *fakeBroker, topic string, items, files int) {
	t.Helper()
	broker := waitForBroker(t, accepted)
	if got := waitForString(t, broker.subs); got != topic {
		t.Fatalf("the container subscribed to %q, want %q", got, topic)
	}
	broker.push(topic, syncReport(t, items, files))
}

func TestAContainerWaitsUntilItsCopyHoldsWhatTheReportCounts(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, accepted := syncingEnricher(t, catalog)
	done := make(chan error, 1)
	go func() { done <- work.awaitCatalogSync(t.Context(), concernProbe) }()

	reportTheCounts(t, accepted, work.statusTopic, 1, 1)
	select {
	case err := <-done:
		t.Fatalf("the wait ended on an empty copy: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	seedProbeGap(t, catalog, work.root, "The Thing (1982)", "The Thing (1982).mkv")

	if err := <-done; err != nil {
		t.Fatalf("the wait failed after the rows landed: %v", err)
	}
}

func TestAContainerWhoseCopyAlreadyMatchesReadsItsGapAtOnce(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, accepted := syncingEnricher(t, catalog)
	seedProbeGap(t, catalog, work.root, "The Thing (1982)", "The Thing (1982).mkv")
	done := make(chan error, 1)
	go func() { done <- work.awaitCatalogSync(t.Context(), concernProbe) }()

	reportTheCounts(t, accepted, work.statusTopic, 1, 1)

	if err := <-done; err != nil {
		t.Fatalf("the wait failed on a copy that already matched: %v", err)
	}
}

func TestAContainerThatHearsNoReportFails(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, _ := syncingEnricher(t, catalog)
	work.syncTimeout = 100 * time.Millisecond

	if err := work.awaitCatalogSync(t.Context(), concernProbe); err == nil {
		t.Error("the wait ended with no report, want a failure")
	}
}

func TestOnlyThisLibrarysReportEndsTheWait(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	sync := newCatalogSync(libraryStatusTopic(defaultTopicBase, "house", "movies"))

	sync.note(libraryStatusTopic(defaultTopicBase, "house", "series"), syncReport(t, 0, 0))
	sync.note(sync.topic, []byte("not a report"))

	synced, err := sync.synced(t.Context(), catalog, "house/movies")
	if err != nil {
		t.Fatal(err)
	}
	if synced {
		t.Error("the wait ended on a message that is not this library's report")
	}
}

func TestAContainerThatCannotReachItsAgentFailsTheWait(t *testing.T) {
	work, accepted := syncingEnricher(t, NewCatalog("http://127.0.0.1:1", &http.Client{Timeout: time.Second}))
	done := make(chan error, 1)
	go func() { done <- work.awaitCatalogSync(t.Context(), concernProbe) }()

	reportTheCounts(t, accepted, work.statusTopic, 1, 1)

	if err := <-done; err == nil {
		t.Error("the wait ended with no count of its own, want the unreachable agent's error")
	}
}

func TestAStoppedContainerEndsItsWait(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, _ := syncingEnricher(t, catalog)
	ctx, stop := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- work.awaitCatalogSync(ctx, concernProbe) }()

	stop()

	if err := <-done; err == nil {
		t.Error("the wait ended cleanly on a stopped container, want a failure")
	}
}

func TestAContainerWithNoBrokerRefusesToWait(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, _ := syncingEnricher(t, catalog)
	t.Setenv(busAddressVariable, "")

	if err := work.awaitCatalogSync(t.Context(), concernProbe); err == nil {
		t.Error("the wait started with no broker, want a refusal")
	}
}

func TestAConcernContainerFailsWhereTheCopyNeverSyncs(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, _ := syncingEnricher(t, catalog)
	work.syncTimeout = 100 * time.Millisecond
	client, _ := newFakeTMDb(t, nil)

	if err := work.probeConcern(t.Context(), answeringProbe(ffprobeOfOneFile)); err == nil {
		t.Error("the probe container read its gap off an unsynced copy")
	}
	if err := work.identityConcern(t.Context(), client); err == nil {
		t.Error("the identity container read its gap off an unsynced copy")
	}
}

func TestTheProbeContainerFillsItsGapOnceTheCopyIsSynced(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, accepted := syncingEnricher(t, catalog)
	folder := "The Thing (1982)"
	seedProbeGap(t, catalog, work.root, folder, "The Thing (1982).mkv")
	done := make(chan error, 1)
	go func() { done <- work.probeConcern(t.Context(), answeringProbe(ffprobeOfOneFile)) }()

	reportTheCounts(t, accepted, work.statusTopic, 1, 1)

	if err := <-done; err != nil {
		t.Fatalf("the probe container failed: %v", err)
	}
	sidecar := readFileString(t, filepath.Join(work.root, folder, movieSidecarName))
	if !strings.Contains(sidecar, "<codec>h264</codec>") {
		t.Errorf("the sidecar holds no stream details:\n%s", sidecar)
	}
}

func TestTheIdentityContainerFillsItsGapOnceTheCopyIsSynced(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	work, accepted := syncingEnricher(t, catalog)
	folder := "The Thing (1982)"
	writeFile(t, filepath.Join(work.root, folder, "thing.mkv"), "video")
	seedIdentityGap(t, catalog, libraryKindMovies, folder, "1982", 0)
	client, _ := newFakeTMDb(t, map[string]string{
		tmdbKey("/3/search/movie", "The Thing", "1982"): `{"results":[` +
			tmdbResultJSON(1091, "The Thing", "1982-06-25") + `]}`,
	})
	done := make(chan error, 1)
	go func() { done <- work.identityConcern(t.Context(), client) }()

	reportTheCounts(t, accepted, work.statusTopic, 1, 0)

	if err := <-done; err != nil {
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
