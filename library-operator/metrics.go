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
	runDuration    *prometheus.HistogramVec
	runLastSuccess *prometheus.GaugeVec
	items          *prometheus.GaugeVec
	// factGap is the rows each fact has left to fill, from the same gap
	// counts the operator schedules the enricher on.
	factGap *prometheus.GaugeVec
	// tallyCounters is the counters the Jobs' own counts feed, by tally
	// metric name.
	tallyCounters map[string]*prometheus.CounterVec

	// lastRun is the newest run this operator has already turned into a
	// duration observation, by library and worker. The pass reads the
	// same report on every backstop tick between two runs, and without
	// this a ten-second tick would observe one finished run's duration
	// dozens of times.
	mutex   sync.Mutex
	lastRun map[runKey]time.Time
	// lastTally is the value this operator last read for one tally row, so a
	// report read again adds nothing and a grown row adds its delta.
	lastTally map[tallyStateKey]float64
}

// runKey is one worker of one library, which is what a run is observed
// under.
type runKey struct {
	library string
	worker  string
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
		runDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "library_run_duration_seconds",
			Help:    "How long one worker's run over a library took, from its start to its finish.",
			Buckets: []float64{1, 5, 15, 30, 60, 120, 300, 600, 1800, 3600},
		}, []string{"library", "worker"}),
		runLastSuccess: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "library_run_last_success_timestamp_seconds",
			Help: "When one worker's last run over a library finished.",
		}, []string{"library", "worker"}),
		items: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "library_items",
			Help: "Item rows the catalog holds for a library, by kind.",
		}, []string{"library", "kind"}),
		factGap: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "library_fact_gap",
			Help: "Rows one fact has left to fill in a library.",
		}, []string{"library", "fact"}),
		tallyCounters: newTallyCounters(factory),
		lastRun:       map[runKey]time.Time{},
		lastTally:     map[tallyStateKey]float64{},
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
// published into the layer 3 rows. A nil report is a Library no reporter
// has spoken for yet, and leaves every series absent rather than a false
// zero: an absent series reads as unknown, and a zero would read as an
// empty library.
//
// The rows are the items by kind, the gap of every fact whose Job this
// Library runs, one run per worker for every finished run the report holds,
// and the counters the report's tallies feed.
func (m *metrics) observeLibraryReport(library *Library, report *libraryReport) {
	if m == nil || library == nil || report == nil {
		return
	}
	name := library.Metadata.Name
	for kind, count := range report.ItemsByKind {
		m.items.WithLabelValues(name, kind).Set(float64(count))
	}
	for fact, count := range report.Gaps {
		// A gap no Job of this Library fills is not published, and the series
		// a switch turned off leaves behind is deleted with it.
		if !libraryRunsFact(library, fact) {
			m.factGap.DeleteLabelValues(name, fact)
			continue
		}
		m.factGap.WithLabelValues(name, fact).Set(float64(count))
	}
	for _, run := range report.Runs {
		m.observeRun(name, run)
	}
	m.observeTallies(name, report.Tallies)
}

// libraryRunsFact reports whether the Library runs the Job that fills one
// fact. The trickplay sheets and the trailer files each have a switch of their
// own, and every other fact runs in the enricher, which every Library runs.
func libraryRunsFact(library *Library, fact string) bool {
	switch fact {
	case factTrickplay:
		return library.Spec.Trickplay.Enabled
	case factTrailerFile:
		return library.Spec.Trailers.Enabled
	}
	return true
}

// observeRun records one finished run: the worker's last success, and its
// duration once. A run this operator has already observed changes nothing, so
// a report read again on the next backstop tick costs nothing here.
func (m *metrics) observeRun(library string, run libraryRun) {
	if run.Started.IsZero() || run.Finished.IsZero() {
		return
	}
	key := runKey{library: library, worker: run.Worker}
	m.mutex.Lock()
	changed := run.Finished.After(m.lastRun[key])
	if changed {
		m.lastRun[key] = run.Finished
	}
	m.mutex.Unlock()
	if !changed {
		return
	}
	m.runLastSuccess.WithLabelValues(library, run.Worker).Set(float64(run.Finished.Unix()))
	m.runDuration.WithLabelValues(library, run.Worker).Observe(run.Finished.Sub(run.Started).Seconds())
}

// dropLibrary removes every series this operator holds for a Library the
// collection no longer holds, the same moment the pass clears that
// Library's bus topics. A metric for a deleted Library would otherwise
// stand at its last value forever.
func (m *metrics) dropLibrary(library string) {
	if m == nil {
		return
	}
	// The label sets a Library has are its workers, its kinds, its facts, and
	// whatever labels its Jobs counted under, so each series is deleted by its
	// library label and not by a list this operator keeps.
	held := prometheus.Labels{"library": library}
	m.runDuration.DeletePartialMatch(held)
	m.runLastSuccess.DeletePartialMatch(held)
	m.items.DeletePartialMatch(held)
	m.factGap.DeletePartialMatch(held)
	for _, counter := range m.tallyCounters {
		counter.DeletePartialMatch(held)
	}
	m.mutex.Lock()
	for key := range m.lastRun {
		if key.library == library {
			delete(m.lastRun, key)
		}
	}
	for key := range m.lastTally {
		if key.library == library {
			delete(m.lastTally, key)
		}
	}
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
