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

// A finished scan run's fields, for a report that names a library. The
// report also carries the counts library_items sums.
func scannedReport(started, finished time.Time) *libraryReport {
	return &libraryReport{
		LastWalk:    finished,
		ItemsByKind: map[string]int{libraryKindMovies: 3, libraryKindSeries: 1},
		Runs:        []libraryRun{{Worker: workerScan, Started: started, Finished: finished}},
	}
}

// The items gauge and the scan gauges read straight off the report a
// Library's namespace published, the same report that already feeds
// that Library's status.
func TestObserveLibraryReportSetsTheLayerThreeGauges(t *testing.T) {
	m := newMetrics("test")
	finished := time.Unix(1_700_000_000, 0).UTC()
	report := scannedReport(finished.Add(-time.Minute), finished)

	m.observeLibraryReport("movies", report)

	if got := testutil.ToFloat64(m.items.WithLabelValues("movies", libraryKindMovies)); got != 3 {
		t.Errorf("items[movies] = %v, want 3", got)
	}
	if got := testutil.ToFloat64(m.items.WithLabelValues("movies", libraryKindSeries)); got != 1 {
		t.Errorf("items[series] = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.scanLastSuccess.WithLabelValues("movies")); got != float64(finished.Unix()) {
		t.Errorf("scan_last_success = %v, want %v", got, finished.Unix())
	}
	body := scrape(t, m)
	if want := `library_scan_duration_seconds_count{library="movies"} 1`; !strings.Contains(body, want) {
		t.Errorf("scrape did not contain %q:\n%s", want, body)
	}
}

// The backstop tick reads the same report between two scans, on every
// pass. The duration histogram observes the finished scan once, not once
// per tick, because a run this operator has already turned into an
// observation moves nothing on a later pass that reads it again.
func TestObserveLibraryReportObservesEachScanOnce(t *testing.T) {
	m := newMetrics("test")
	finished := time.Unix(1_700_000_000, 0).UTC()
	report := scannedReport(finished.Add(-time.Minute), finished)

	m.observeLibraryReport("movies", report)
	m.observeLibraryReport("movies", report)
	m.observeLibraryReport("movies", report)

	body := scrape(t, m)
	if want := `library_scan_duration_seconds_count{library="movies"} 1`; !strings.Contains(body, want) {
		t.Errorf("scrape did not contain %q:\n%s", want, body)
	}
}

// A later scan's finish moves the gauge and observes a second duration,
// because it is a run this operator has not already turned into an
// observation.
func TestObserveLibraryReportObservesALaterScan(t *testing.T) {
	m := newMetrics("test")
	first := time.Unix(1_700_000_000, 0).UTC()
	m.observeLibraryReport("movies", scannedReport(first.Add(-time.Minute), first))

	second := first.Add(time.Hour)
	m.observeLibraryReport("movies", scannedReport(second.Add(-time.Minute), second))

	if got := testutil.ToFloat64(m.scanLastSuccess.WithLabelValues("movies")); got != float64(second.Unix()) {
		t.Errorf("scan_last_success = %v, want the later scan's finish %v", got, second.Unix())
	}
	body := scrape(t, m)
	if want := `library_scan_duration_seconds_count{library="movies"} 2`; !strings.Contains(body, want) {
		t.Errorf("scrape did not contain %q:\n%s", want, body)
	}
}

// A Library with no report yet, and a Library whose report carries no
// finished scan run, set no gauge at all: an absent series is unknown,
// where a zero would read as an empty library.
func TestObserveLibraryReportLeavesAbsentSeriesAlone(t *testing.T) {
	m := newMetrics("test")

	m.observeLibraryReport("new", nil)
	m.observeLibraryReport("scanning", &libraryReport{
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
	m.observeLibraryReport("gone", scannedReport(finished.Add(-time.Minute), finished))

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
	m.observeLibraryReport("library", &libraryReport{
		ItemsByKind: map[string]int{libraryKindMovies: 1},
		Runs:        []libraryRun{{Worker: workerScan, Started: time.Now(), Finished: time.Now()}},
	})
	m.dropLibrary("library")
}
