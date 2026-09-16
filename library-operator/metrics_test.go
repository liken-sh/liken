package main

// These tests prove the operator's Prometheus wiring against a real
// registry, scraped through promhttp the way a Prometheus server would,
// and against a nil *metrics, the disabled state every recording call
// site runs under when the cluster names no listener address.

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// scrape reads the registry through promhttp, the same handler the
// listener serves, so a test proves what a real scrape would read.
func scrape(t *testing.T, m *metrics) string {
	t.Helper()
	server := httptest.NewServer(promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	t.Cleanup(server.Close)
	resp, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// Layer 1: every process in the organization carries this one gauge, so
// a build's version reaches Prometheus with no second source for it.
func TestBuildInfoReportsTheComponentAndVersion(t *testing.T) {
	m := newMetrics("2026.09.10-001")

	body := scrape(t, m)

	want := `liken_build_info{component="library-operator",version="2026.09.10-001"} 1`
	if !strings.Contains(body, want) {
		t.Errorf("scrape did not contain %q:\n%s", want, body)
	}
}

// A reconcile pass that changes nothing still counts as a run, and only
// a pass that ends in an error counts against the error total.
func TestObserveReconcileCountsDurationAndErrors(t *testing.T) {
	m := newMetrics("test")

	m.observeReconcile(kindLibrary, 10*time.Millisecond, nil)
	m.observeReconcile(kindLibrary, 20*time.Millisecond, errors.New("a reconcile problem"))

	if got := testutil.ToFloat64(m.reconcileErrors.WithLabelValues(kindLibrary)); got != 1 {
		t.Errorf("reconcile_errors_total = %v, want 1", got)
	}
	body := scrape(t, m)
	if want := `library_reconcile_duration_seconds_count{kind="Library"} 2`; !strings.Contains(body, want) {
		t.Errorf("scrape did not contain %q:\n%s", want, body)
	}
}

// A scrape reads the registry and never changes it: the same two passes
// read twice must report the same counter, because a scrape is an
// observation and not an event of its own.
func TestRepeatedScrapesLeaveCountersUnchanged(t *testing.T) {
	m := newMetrics("test")
	m.observeReconcile(kindLibrary, time.Millisecond, errors.New("a reconcile problem"))

	first := scrape(t, m)
	second := scrape(t, m)

	want := `library_reconcile_errors_total{kind="Library"} 1`
	if !strings.Contains(first, want) || !strings.Contains(second, want) {
		t.Errorf("scrapes = %q then %q, want %q in both", first, second, want)
	}
}

// A watch that reconnects counts one restart, under the kind the watch
// serves, and a scrape never advances it.
func TestRecordWatchRestartCounts(t *testing.T) {
	m := newMetrics("test")

	m.recordWatchRestart(kindPod)
	m.recordWatchRestart(kindPod)

	if got := testutil.ToFloat64(m.watchRestarts.WithLabelValues(kindPod)); got != 2 {
		t.Errorf("watch_restarts_total = %v, want 2", got)
	}
}

// libraryNamed builds the Library whose name the series hold, with every
// fact's Job off.
func libraryNamed(name string) *Library {
	return &Library{Metadata: ObjectMeta{Namespace: "house", Name: name}}
}

// scannedReport builds a finished scan run's fields, for a report that names
// a library. The report also holds the counts library_items sums, and one gap
// count, which library_fact_gap reads.
func scannedReport(started, finished time.Time) *libraryReport {
	return &libraryReport{
		LastWalk:    finished,
		ItemsByKind: map[string]int{libraryKindMovies: 3, libraryKindSeries: 1},
		Gaps:        map[string]int{factProbe: 7},
		Runs:        []libraryRun{{Worker: workerScan, Started: started, Finished: finished}},
	}
}

// The items gauge and the run gauges read straight off the report a
// Library's namespace published, the same report that already feeds that
// Library's status.
//
// The gap gauge reads the same report's gap counts.
func TestObserveLibraryReportSetsTheLayerThreeGauges(t *testing.T) {
	m := newMetrics("test")
	finished := time.Unix(1_700_000_000, 0).UTC()
	report := scannedReport(finished.Add(-time.Minute), finished)

	m.observeLibraryReport(libraryNamed("movies"), report)

	if got := testutil.ToFloat64(m.items.WithLabelValues("movies", libraryKindMovies)); got != 3 {
		t.Errorf("items[movies] = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.items.WithLabelValues("movies", libraryKindSeries)); got != 1 {
		t.Errorf("items[series] = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.factGap.WithLabelValues("movies", factProbe)); got != 7 {
		t.Errorf("fact_gap[probe] = %v, want 7", got)
	}
	if got := testutil.ToFloat64(m.runLastSuccess.WithLabelValues("movies", workerScan)); got != float64(finished.Unix()) {
		t.Errorf("run_last_success = %v, want %v", got, finished.Unix())
	}
	body := scrape(t, m)
	if want := `library_run_duration_seconds_count{library="movies",worker="scan"} 1`; !strings.Contains(body, want) {
		t.Errorf("scrape did not contain %q:\n%s", want, body)
	}
}

// libraryWithFacts builds one Library with the trickplay and the trailers
// switches a test names.
func libraryWithFacts(trickplay, trailers bool) *Library {
	library := libraryNamed("movies")
	library.Spec.Trickplay.Enabled = trickplay
	library.Spec.Trailers.Enabled = trailers
	return library
}

// The gap of a fact whose Job the Library has turned off is in no scrape.
func TestTheGapOfAFactWithNoJobIsNotPublished(t *testing.T) {
	cases := []struct {
		name      string
		library   *Library
		fact      string
		published bool
	}{
		{name: "trickplay on", library: libraryWithFacts(true, false),
			fact: factTrickplay, published: true},
		{name: "trickplay off", library: libraryWithFacts(false, false),
			fact: factTrickplay, published: false},
		{name: "trailer files on", library: libraryWithFacts(false, true),
			fact: factTrailerFile, published: true},
		{name: "trailer files off", library: libraryWithFacts(false, false),
			fact: factTrailerFile, published: false},
		{name: "a fact every library runs", library: libraryWithFacts(false, false),
			fact: factProbe, published: true},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			m := newMetrics("test")

			m.observeLibraryReport(one.library, &libraryReport{Gaps: map[string]int{one.fact: 7}})

			body := scrape(t, m)
			series := `library_fact_gap{fact="` + one.fact + `",library="movies"} 7`
			if strings.Contains(body, series) != one.published {
				t.Errorf("the scrape carries %q = %v, want %v:\n%s",
					series, !one.published, one.published, body)
			}
		})
	}
}

// Turning a fact's Job off clears the series the fact left behind while its
// Job was on.
func TestTurningAFactsJobOffClearsItsGap(t *testing.T) {
	m := newMetrics("test")
	report := &libraryReport{Gaps: map[string]int{factTrickplay: 7}}
	m.observeLibraryReport(libraryWithFacts(true, false), report)

	m.observeLibraryReport(libraryWithFacts(false, false), report)

	body := scrape(t, m)
	if strings.Contains(body, "library_fact_gap{") {
		t.Errorf("the scrape still carries the gap of a fact with no Job:\n%s", body)
	}
}

// Every worker that finished a run is observed under its own name, so one
// library has a duration for its scan and one for its enricher.
func TestObserveLibraryReportObservesEveryWorkersRun(t *testing.T) {
	m := newMetrics("test")
	finished := time.Unix(1_700_000_000, 0).UTC()
	report := scannedReport(finished.Add(-time.Minute), finished)
	report.Runs = append(report.Runs, libraryRun{
		Worker: workerEnrich, Started: finished.Add(-2 * time.Minute), Finished: finished,
	})

	m.observeLibraryReport(libraryNamed("movies"), report)

	body := scrape(t, m)
	for _, want := range []string{
		`library_run_duration_seconds_count{library="movies",worker="scan"} 1`,
		`library_run_duration_seconds_count{library="movies",worker="enrich"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape did not contain %q:\n%s", want, body)
		}
	}
}

// The backstop tick reads the same report between two runs, on every
// pass. The duration histogram observes the finished run once, not once
// per tick, because a run this operator has already turned into an
// observation moves nothing on a later pass that reads it again.
func TestObserveLibraryReportObservesEachRunOnce(t *testing.T) {
	m := newMetrics("test")
	finished := time.Unix(1_700_000_000, 0).UTC()
	report := scannedReport(finished.Add(-time.Minute), finished)

	m.observeLibraryReport(libraryNamed("movies"), report)
	m.observeLibraryReport(libraryNamed("movies"), report)
	m.observeLibraryReport(libraryNamed("movies"), report)

	body := scrape(t, m)
	if want := `library_run_duration_seconds_count{library="movies",worker="scan"} 1`; !strings.Contains(body, want) {
		t.Errorf("scrape did not contain %q:\n%s", want, body)
	}
}

// A later run's finish moves the gauge and observes a second duration,
// because it is a run this operator has not already turned into an
// observation.
func TestObserveLibraryReportObservesALaterRun(t *testing.T) {
	m := newMetrics("test")
	first := time.Unix(1_700_000_000, 0).UTC()
	m.observeLibraryReport(libraryNamed("movies"), scannedReport(first.Add(-time.Minute), first))

	second := first.Add(time.Hour)
	m.observeLibraryReport(libraryNamed("movies"), scannedReport(second.Add(-time.Minute), second))

	if got := testutil.ToFloat64(m.runLastSuccess.WithLabelValues("movies", workerScan)); got != float64(second.Unix()) {
		t.Errorf("run_last_success = %v, want the later scan's finish %v", got, second.Unix())
	}
	body := scrape(t, m)
	if want := `library_run_duration_seconds_count{library="movies",worker="scan"} 2`; !strings.Contains(body, want) {
		t.Errorf("scrape did not contain %q:\n%s", want, body)
	}
}

// A Library with no report yet, and a Library whose report carries no
// finished scan run, set no gauge at all: an absent series is unknown,
// where a zero would read as an empty library.
func TestObserveLibraryReportLeavesAbsentSeriesAlone(t *testing.T) {
	m := newMetrics("test")

	m.observeLibraryReport(libraryNamed("new"), nil)
	m.observeLibraryReport(libraryNamed("scanning"), &libraryReport{
		Runs: []libraryRun{{Worker: workerScan, Started: time.Now()}},
	})

	body := scrape(t, m)
	for _, absent := range []string{`library="new"`, `library="scanning"`} {
		if strings.Contains(body, absent) {
			t.Errorf("scrape carries %q, want no series for a library with no finished scan", absent)
		}
	}
}

// A departed Library takes its series with it, the same moment the pass
// clears its bus topics, so a deleted Library's last count does not
// stand forever.
func TestDropLibraryRemovesItsSeries(t *testing.T) {
	m := newMetrics("test")
	finished := time.Unix(1_700_000_000, 0).UTC()
	m.observeLibraryReport(libraryNamed("gone"), scannedReport(finished.Add(-time.Minute), finished))

	m.dropLibrary("gone")

	body := scrape(t, m)
	if strings.Contains(body, `library="gone"`) {
		t.Errorf("scrape still carries the dropped library:\n%s", body)
	}
}

// Every recording method answers a nil *metrics as a no-op, which is the
// state every call site runs under when the cluster names no listener
// address, so a pass that records against it costs one branch and
// nothing else.
func TestNilMetricsRecordingMethodsAreNoOps(t *testing.T) {
	var m *metrics

	m.observeReconcile(kindLibrary, time.Second, errors.New("a reconcile problem"))
	m.recordWatchRestart(kindLibrary)
	m.observeLibraryReport(libraryNamed("library"), &libraryReport{
		ItemsByKind: map[string]int{libraryKindMovies: 1},
		Runs:        []libraryRun{{Worker: workerScan, Started: time.Now(), Finished: time.Now()}},
	})
	m.dropLibrary("library")
}
