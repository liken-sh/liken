package main

// What these tests prove: the reports the namespace's one reporter
// builds from a catalog loaded with the shipped schema, and the messages
// it leaves on a broker, with no pod and no agent.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The reporter reads the namespace it serves, the bus, and the
// agent's address out of the environment, and falls back to the loopback
// agent and the default topic base.
func TestNewReporterReadsItsEnvironment(t *testing.T) {
	t.Setenv(libraryNamespaceVariable, "house")
	t.Setenv(topicBaseVariable, "")
	t.Setenv(catalogAPIVariable, "")
	t.Setenv(busAddressVariable, "")
	var logged strings.Builder

	report := newReporter(&logged)

	if report.namespace != "house" {
		t.Errorf("namespace = %q, want house", report.namespace)
	}
	if report.catalog.base != defaultCatalogAPI {
		t.Errorf("catalog = %q, want the loopback agent %s", report.catalog.base, defaultCatalogAPI)
	}
	if want := catalogAvailabilityTopic(defaultTopicBase, "house"); report.availabilityTopic != want {
		t.Errorf("availability topic = %q, want %q", report.availabilityTopic, want)
	}
	if !strings.Contains(logged.String(), "house") {
		t.Errorf("log = %q, want the namespace it reports on", logged.String())
	}
}

// A catalog with two libraries, one walked and one still walking,
// which is the state a reporter reads on a running cluster.
func seededReporter(t *testing.T) (*reporter, *Catalog) {
	t.Helper()
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	report := &reporter{
		namespace:         "house",
		topicBase:         defaultTopicBase,
		availabilityTopic: catalogAvailabilityTopic(defaultTopicBase, "house"),
		catalog:           catalog,
		log:               io.Discard,
		published:         map[string]libraryReport{},
	}
	return report, catalog
}

// The report carries the catalog's own counts and the runs of
// every worker, and the scan run is what says when the volume was last
// walked and what that walk left.
func TestTheReportCarriesTheCountsAndTheRuns(t *testing.T) {
	report, catalog := seededReporter(t)
	walked := time.Unix(1_700_000_000, 0).UTC()
	if err := catalog.UpsertRun(t.Context(), "house/movies", libraryRun{
		Worker: workerScan, Job: "scan-1", Started: walked.Add(-time.Minute), Finished: walked,
		Unidentified: 4, Removed: 9,
	}); err != nil {
		t.Fatal(err)
	}

	built, err := report.buildReport(t.Context(), "house/movies")
	if err != nil {
		t.Fatal(err)
	}

	if built.Titles != 2 || built.Items != 3 || built.Files != 2 {
		t.Errorf("report = %+v, want the catalog's own counts", built)
	}
	if len(built.Runs) != 1 || built.Runs[0].Job != "scan-1" {
		t.Errorf("runs = %+v, want the scan run", built.Runs)
	}
	if !built.LastWalk.Equal(walked) {
		t.Errorf("lastWalk = %v, want the finish of the scan run %v", built.LastWalk, walked)
	}
	if built.Unidentified != 4 || built.RemovedLastSweep != 9 {
		t.Errorf("report = %+v, want the counts the scan run carries", built)
	}
	if built.Walking {
		t.Error("the report says a walk is running after the run finished")
	}
}

// A scan run that started after it last finished is a walk in
// flight, which is how the operator's phase follows a Job.
func TestAScanRunThatHasNotFinishedIsAWalkInFlight(t *testing.T) {
	report, catalog := seededReporter(t)
	if err := catalog.UpsertRun(t.Context(), "house/movies",
		libraryRun{Worker: workerScan, Job: "scan-2", Started: time.Unix(1_700_000_000, 0)}); err != nil {
		t.Fatal(err)
	}

	built, err := report.buildReport(t.Context(), "house/movies")
	if err != nil {
		t.Fatal(err)
	}

	if !built.Walking {
		t.Error("the report of a running scan does not say a walk is in flight")
	}
	if !built.LastWalk.IsZero() {
		t.Errorf("lastWalk = %v, want no walk time from a run that has not finished", built.LastWalk)
	}
}

// A library with no run reports its counts and no walk, which is
// the catalog a reporter reads before the first Job runs.
func TestALibraryWithNoRunStillReportsItsCounts(t *testing.T) {
	report, _ := seededReporter(t)

	built, err := report.buildReport(t.Context(), "house/series")
	if err != nil {
		t.Fatal(err)
	}

	if built.Items != 3 {
		t.Errorf("items = %d, want the catalog's own count", built.Items)
	}
	if len(built.Runs) != 0 || built.Walking {
		t.Errorf("report = %+v, want no run and no walk", built)
	}
}

// The last-change time moves only when the counts move, so a
// rebuild that reads the same rows carries the time the change did.
func TestTheLastChangeTimeMovesOnlyWithTheCounts(t *testing.T) {
	report, catalog := seededReporter(t)
	ctx := t.Context()

	first, err := report.buildReport(ctx, "house/movies")
	if err != nil {
		t.Fatal(err)
	}
	report.published["house/movies"] = first

	again, err := report.buildReport(ctx, "house/movies")
	if err != nil {
		t.Fatal(err)
	}
	if !again.LastChange.Equal(first.LastChange) {
		t.Errorf("lastChange = %v, want the unchanged %v", again.LastChange, first.LastChange)
	}

	if _, err := catalog.DeleteMovies(ctx, "house/movies", []string{"movie:tmdb:1"}); err != nil {
		t.Fatal(err)
	}
	moved, err := report.buildReport(ctx, "house/movies")
	if err != nil {
		t.Fatal(err)
	}
	if !moved.LastChange.After(first.LastChange) {
		t.Errorf("lastChange = %v, want a time past %v", moved.LastChange, first.LastChange)
	}
}

// A read that fails leaves the retained report where it is, so an
// agent that stops answering never reads as an empty library.
func TestAFailedReadPublishesNothing(t *testing.T) {
	unwell := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(unwell.Close)
	report, _ := seededReporter(t)
	report.catalog = NewCatalog(unwell.URL, unwell.Client())
	var logged strings.Builder
	report.log = &logged

	if _, err := report.buildReport(t.Context(), "house/movies"); err == nil {
		t.Fatal("the build returned no error, want the failed read")
	}

	report.publishEveryLibrary(t.Context())
	if !strings.Contains(logged.String(), "could not read the libraries") {
		t.Errorf("log = %q, want the failed read named", logged.String())
	}
}

// A library key that is not a namespace and a name names no topic,
// so the reporter publishes nothing for it.
func TestSplitLibraryKey(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		namespace string
		want      bool
	}{
		{name: "a namespace and a name", key: "house/movies", namespace: "house", want: true},
		{name: "no separator", key: "movies"},
		{name: "no namespace", key: "/movies"},
		{name: "no name", key: "house/"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			namespace, _, ok := splitLibraryKey(testCase.key)
			if ok != testCase.want || namespace != testCase.namespace {
				t.Errorf("splitLibraryKey(%q) = %q, %v, want %q, %v",
					testCase.key, namespace, ok, testCase.namespace, testCase.want)
			}
		})
	}
}

// One reporter over a seeded catalog and the test broker, so the
// retained reports and the availability travel a real connection.
func servingReporter(t *testing.T, catalog *Catalog) (*reporter, <-chan *fakeBroker, context.CancelFunc) {
	t.Helper()
	address, accepted := testBroker(t)
	shorterBackoff(t)
	graceWas := reportFlushGrace
	t.Cleanup(func() { reportFlushGrace = graceWas })
	reportFlushGrace = 5 * time.Millisecond
	backoffWas := reportMinBackoff
	t.Cleanup(func() { reportMinBackoff = backoffWas })
	reportMinBackoff = 5 * time.Millisecond

	report := &reporter{
		namespace:         "house",
		topicBase:         defaultTopicBase,
		availabilityTopic: catalogAvailabilityTopic(defaultTopicBase, "house"),
		catalog:           catalog,
		log:               io.Discard,
		published:         map[string]libraryReport{},
	}
	report.bus = newBus(address, "catalog-house",
		&busWill{Topic: report.availabilityTopic, Payload: []byte(availabilityOffline), Retained: true},
		report.onConnect, nil)

	stopped, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		report.serve(stopped)
	}()
	t.Cleanup(func() {
		stop()
		<-done
	})
	return report, accepted, stop
}

// The reporter publishes online retained and one retained report
// per library the catalog holds, so a broker that restarts is refilled
// and a subscriber that arrives later reads the current counts.
func TestTheReporterPublishesOnlineAndEveryLibrary(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	report, accepted, _ := servingReporter(t, catalog)
	broker := waitForBroker(t, accepted)

	online := waitForTopic(t, broker, report.availabilityTopic)
	if string(online.payload) != availabilityOnline || !online.retained {
		t.Errorf("availability = %q retained %v, want a retained online", online.payload, online.retained)
	}

	movies := waitForTopic(t, broker, libraryStatusTopic(defaultTopicBase, "house", "movies"))
	if !movies.retained {
		t.Error("the report was not retained")
	}
	var held libraryReport
	if err := json.Unmarshal(movies.payload, &held); err != nil {
		t.Fatal(err)
	}
	if held.Items != 3 {
		t.Errorf("items = %d, want the catalog's own count", held.Items)
	}
	waitForTopic(t, broker, libraryStatusTopic(defaultTopicBase, "house", "series"))
}

// A run that lands republishes that library's report, so a Job
// hears its own echo within the stream's latency and nothing polls.
func TestTheReporterRepublishesOnARun(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	_, accepted, _ := servingReporter(t, catalog)
	broker := waitForBroker(t, accepted)
	topic := libraryStatusTopic(defaultTopicBase, "house", "movies")
	waitForTopic(t, broker, topic)

	if err := catalog.UpsertRun(t.Context(), "house/movies", libraryRun{
		Worker: workerScan, Job: "scan-7",
		Started: time.Unix(10, 0), Finished: time.Unix(20, 0),
	}); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(scanTestTimeout)
	for {
		select {
		case <-deadline:
			t.Fatal("no report named the run that landed")
		case published := <-broker.pubs:
			if published.topic != topic {
				continue
			}
			var held libraryReport
			if err := json.Unmarshal(published.payload, &held); err != nil {
				t.Fatal(err)
			}
			if run, has := runOf(held.Runs, workerScan); has && run.Job == "scan-7" {
				return
			}
		}
	}
}

// The kubelet stops the pod with SIGTERM, and the reporter marks
// itself offline before it returns, so a catalog pod that leaves is not
// read as one that is still reporting.
func TestTheReporterPublishesOfflineOnItsWayOut(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	report, accepted, stop := servingReporter(t, catalog)
	broker := waitForBroker(t, accepted)
	waitForTopic(t, broker, report.availabilityTopic)

	stop()

	got := waitForTopic(t, broker, report.availabilityTopic)
	if string(got.payload) != availabilityOffline || !got.retained {
		t.Errorf("availability = %q retained %v, want a retained offline", got.payload, got.retained)
	}
}

// An agent that refuses the stream is retried, so a reporter that
// comes up before its agent does still reports.
func TestTheReporterOpensTheStreamAgain(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	attempts := make(chan struct{}, 8)
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, subscriptionsPath) {
			select {
			case attempts <- struct{}{}:
			default:
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, catalog.base+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(refusing.Close)

	servingReporter(t, NewCatalog(refusing.URL, refusing.Client()))

	for range 2 {
		select {
		case <-attempts:
		case <-time.After(scanTestTimeout):
			t.Fatal("the reporter did not open the stream again")
		}
	}
}

// A read of the catalog that fails leaves the report unbuilt,
// whichever of the four reads it is, so a half-read library is never
// published as a whole one.
func TestAReportIsNotBuiltFromAFailedRead(t *testing.T) {
	cases := []struct {
		name string
		read int
	}{
		{name: "the runs", read: 1},
		{name: "the titles", read: 2},
		{name: "the items", read: 3},
		{name: "the files", read: 4},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			report, catalog := seededReporter(t)
			served := 0
			report.catalog = proxyCatalog(t, catalog, func(path string, body []byte) bool {
				if !strings.HasSuffix(path, queriesPath) {
					return false
				}
				served++
				return served == testCase.read
			})

			if _, err := report.buildReport(t.Context(), "house/movies"); err == nil {
				t.Error("the build returned no error, want the failed read")
			}
		})
	}
}

// A broker that restarts drops its retained set, so a reconnect
// publishes online and every report the reporter holds again.
func TestTheReporterRefillsTheBrokerOnEveryConnect(t *testing.T) {
	catalog, _ := newSQLiteCatalog(t)
	seedTwoLibrariesInEveryTable(t, catalog)
	report, accepted, _ := servingReporter(t, catalog)
	broker := waitForBroker(t, accepted)
	waitForTopic(t, broker, libraryStatusTopic(defaultTopicBase, "house", "series"))
	report.mutex.Lock()
	report.published["a key that names no library"] = libraryReport{}
	report.mutex.Unlock()

	report.onConnect(report.bus)

	got := waitForTopic(t, broker, report.availabilityTopic)
	if string(got.payload) != availabilityOnline {
		t.Errorf("availability = %q, want %q", got.payload, availabilityOnline)
	}
	waitForTopic(t, broker, libraryStatusTopic(defaultTopicBase, "house", "movies"))
}

// A reporter built with no log writes nowhere, which is what a
// test that wants no output builds.
func TestAReporterWithNoLogWritesNowhere(t *testing.T) {
	report := &reporter{}

	report.logf("nothing reads this")
}
