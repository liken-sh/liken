package main

// metrics.go serves this operator's Prometheus registry: milestone 65's
// layer 1 and layer 2, and the layer 3 rows plans/37-prometheus-metrics.md
// states for this operator. The listener is optional, on its own port from
// the milestone's table, and a failure in it never touches a reconcile
// pass: a *metrics answers every recording method as a no-op when the
// process runs with no listener address, so the pass that calls them
// carries no branch of its own.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// The setting that names the listener's address, read the way every other
// setting is: METRICS_LISTEN_ADDRESS is milestone 65's own name, and the
// media browser in this repository answers to the same variable. An empty
// value serves no metrics, so a cluster that wants none pays nothing for
// the registry or the listener.
const metricsAddressVariable = "METRICS_LISTEN_ADDRESS"

// The component name milestone 65's liken_build_info gauge carries for
// this process.
const metricsComponent = "library-operator"

// metricsReadHeaderTimeout bounds how long the listener waits for a
// scraper's headers, so a connection that opens and says nothing cannot
// hold a slot open.
const metricsReadHeaderTimeout = 10 * time.Second

// The resource kinds this operator's layer 2 series label. Watch and
// reconcile share the set, because both name the kind of object the loop
// is working on.
const (
	kindLibrary          = "Library"
	kindCatalog          = "Catalog"
	kindPod              = "Pod"
	kindPlayer           = "Player"
	kindMediaPreferences = "MediaPreferences"
	kindMetadataProvider = "MetadataProvider"
	kindPlay             = "Play"
	kindPerson           = "Person"
)

// metrics holds every series this operator publishes. A nil *metrics is
// the disabled state: every method below checks for it first and returns,
// so a pass that records against a nil metrics costs one branch and
// nothing else.
type metrics struct {
	registry *prometheus.Registry

	// Layer 2: the reconcile loop under library_, labeled by the
	// resource kind the loop was working on.
	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	watchRestarts     *prometheus.CounterVec

	// Layer 3: the rows plans/37-prometheus-metrics.md states for this
	// operator. Each reads a fact that already reaches this operator over
	// the bus and already feeds a Library's status.
	scanDuration    *prometheus.HistogramVec
	scanLastSuccess *prometheus.GaugeVec
	items           *prometheus.GaugeVec

	// lastScan is the newest scan run this operator has already turned
	// into a duration observation, by library name. The pass reads the
	// same report on every backstop tick between two scans, and without
	// this a ten-second tick would observe one finished scan's duration
	// dozens of times.
	mutex    sync.Mutex
	lastScan map[string]time.Time
}

// newMetrics builds the registry and every series on it, and sets
// liken_build_info once: every process in the organization carries this
// one gauge, so a single panel shows every component's release.
func newMetrics(version string) *metrics {
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	factory := promauto.With(registry)

	buildInfo := factory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "The component and version of this process. Always 1; the labels carry the fact.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(metricsComponent, version).Set(1)

	return &metrics{
		registry: registry,
		reconcileDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "library_reconcile_duration_seconds",
			Help:    "How long one reconcile pass over one object took, by resource kind.",
			Buckets: prometheus.DefBuckets,
		}, []string{"kind"}),
		reconcileErrors: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "library_reconcile_errors_total",
			Help: "Reconcile passes that ended in an error, by resource kind.",
		}, []string{"kind"}),
		watchRestarts: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "library_watch_restarts_total",
			Help: "Watches the API server closed that this operator reopened, by resource kind.",
		}, []string{"kind"}),
		scanDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "library_scan_duration_seconds",
			Help:    "How long a library's full walk took, from its scan run's start to its finish.",
			Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600},
		}, []string{"library"}),
		scanLastSuccess: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "library_scan_last_success_timestamp_seconds",
			Help: "When a library's last full walk finished.",
		}, []string{"library"}),
		items: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "library_items",
			Help: "Item rows the catalog holds for a library, by kind.",
		}, []string{"library", "kind"}),
		lastScan: map[string]time.Time{},
	}
}

// observeReconcile records one pass over one object: how long it took,
// under the resource kind the loop was working on, and whether it ended
// in an error. A reconcile that changed nothing still counts as a run.
func (m *metrics) observeReconcile(kind string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	m.reconcileDuration.WithLabelValues(kind).Observe(duration.Seconds())
	if err != nil {
		m.reconcileErrors.WithLabelValues(kind).Inc()
	}
}

// recordWatchRestart counts one watch the API server closed that this
// operator reopened, under the resource kind the watch serves.
func (m *metrics) recordWatchRestart(kind string) {
	if m == nil {
		return
	}
	m.watchRestarts.WithLabelValues(kind).Inc()
}

// observeLibraryReport folds the newest report a Library's namespace
// published into the layer 3 rows: the items by kind, always, and the
// scan's last success and duration where the report carries a finished
// scan run. A nil report is a Library no reporter has spoken for yet, and
// leaves every series absent rather than a false zero: an absent series
// reads as unknown, and a zero would read as an empty library.
func (m *metrics) observeLibraryReport(library string, report *libraryReport) {
	if m == nil || report == nil {
		return
	}
	for kind, count := range report.ItemsByKind {
		m.items.WithLabelValues(library, kind).Set(float64(count))
	}
	if !report.LastWalk.IsZero() {
		m.scanLastSuccess.WithLabelValues(library).Set(float64(report.LastWalk.Unix()))
	}

	run, held := runOf(report.Runs, workerScan)
	if !held || run.Started.IsZero() || run.Finished.IsZero() {
		return
	}
	m.mutex.Lock()
	changed := run.Finished.After(m.lastScan[library])
	if changed {
		m.lastScan[library] = run.Finished
	}
	m.mutex.Unlock()
	// Only a scan this operator has not already turned into an
	// observation moves the histogram, so a report read again on the next
	// backstop tick, before the next scan finishes, costs nothing here.
	if changed {
		m.scanDuration.WithLabelValues(library).Observe(run.Finished.Sub(run.Started).Seconds())
	}
}

// dropLibrary removes every series this operator holds for a Library the
// collection no longer holds, the same moment the pass clears that
// Library's bus topics. A metric for a deleted Library would otherwise
// stand at its last value forever.
func (m *metrics) dropLibrary(library string) {
	if m == nil {
		return
	}
	m.scanDuration.DeleteLabelValues(library)
	m.scanLastSuccess.DeleteLabelValues(library)
	for _, kind := range itemKinds {
		m.items.DeleteLabelValues(library, kind)
	}
	m.mutex.Lock()
	delete(m.lastScan, library)
	m.mutex.Unlock()
}

// serve answers /metrics from the registry until ctx ends, then shuts the
// listener down. A failure here is returned and never blocks the
// reconcile loop: the caller logs it and carries on, because a cluster
// that cannot be watched still has libraries to scan.
func (m *metrics) serve(ctx context.Context, address string) error {
	server := &http.Server{
		Addr:              address,
		Handler:           promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}),
		ReadHeaderTimeout: metricsReadHeaderTimeout,
	}
	go func() {
		<-ctx.Done()
		ending, done := context.WithTimeout(context.Background(), passTimeout)
		defer done()
		if err := server.Shutdown(ending); err != nil {
			fmt.Fprintf(os.Stderr, "shutting the metrics server down: %v\n", err)
		}
	}()
	if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
