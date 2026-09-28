// Package metrics is liken's half of the Prometheus contract.
//
// A Machine's status says what a machine is now. It says nothing
// about the past: how long the last upgrade took, how often a
// download failed, or what the fleet's release skew was an hour ago.
// Prometheus keeps that history, because a scraper reads the same
// numbers every few seconds and stores each reading with its time.
// plans/completed/65-prometheus-metrics.md is the contract that every liken
// repository follows, and this package is liken's half of it.
//
// The contract has three layers. Layer 1 is the runtime: the client
// library's go_* and process_* series, and one liken_build_info
// gauge that names the component and the release it runs. Layer 2 is
// the reconcile loop, which every operator has: how long a pass
// takes, how many passes fail, and how often a watch restarts, each
// labeled by the resource kind the loop serves. Layer 3 is the
// domain, and it belongs to each program. The machine operator's
// layer 3 is in machine-operator/metrics.go, and the cluster
// operator's is in cluster-operator/metrics.go.
//
// The plan holds layers 1 and 2 to be about fifty lines that each
// repository writes for itself, because the operators in the other
// repositories import nothing from each other. Inside liken, the two
// operators are two programs of one Go module, so they share this
// package the way they already share api/ and kubernetes/.
//
// Every collector here lives on a registry that this package builds,
// never on the client library's global default. A registry that a
// program builds is a value that a test builds too, so every series
// below is proven by a real scrape of a real registry.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Prefix is the name liken puts in front of every metric it
// publishes. The contract gives one prefix to each repository, so a
// person reading a dashboard knows which program a series comes
// from, and two repositories can never name two different facts the
// same thing.
const Prefix = "liken_"

// An Operator is one operator process's registry: layer 1, layer 2,
// and whatever layer 3 the program registers on it. Each operator
// builds exactly one, and hands it to the parts of itself that
// observe something.
type Operator struct {
	registry          *prometheus.Registry
	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	watchRestarts     *prometheus.CounterVec
}

// reconcileBuckets are the histogram's boundaries, in seconds. A
// reconcile pass that changes nothing takes about a millisecond, and
// a pass that writes status takes a few. The buckets step by four,
// from one millisecond to sixteen seconds, so the ordinary pass and
// a pass that waits on a slow API server both land inside the range.
// A pass slower than the top bucket still counts, in the +Inf bucket
// that every Prometheus histogram carries.
var reconcileBuckets = prometheus.ExponentialBuckets(0.001, 4, 8)

// NewOperator builds the registry for one operator process.
// component is the program's own name, and version is the release it
// was built as. reconciles names every resource kind whose loop this
// program runs, and watches names every kind it holds a watch on.
// The two lists differ where an operator reconciles one kind from a
// watch on another: the cluster operator writes the `Cluster`, and
// hears about the fleet on a `Machine` watch.
//
// The kinds matter at construction, not only at observation time.
// Prometheus computes a rate from two readings of the same series,
// so a counter that appears only when it first increases has no
// earlier reading to compare against, and its first error is invisible
// on a graph. Creating the series here publishes each one at zero
// from the first scrape. A kind that a program never serves is left
// out, so a series that is always zero never appears at all.
func NewOperator(component, version string, reconciles, watches []string) *Operator {
	o := &Operator{
		registry: prometheus.NewRegistry(),
		reconcileDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    Prefix + "reconcile_duration_seconds",
			Help:    "How long one reconcile pass takes, by resource kind.",
			Buckets: reconcileBuckets,
		}, []string{"kind"}),
		reconcileErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: Prefix + "reconcile_errors_total",
			Help: "Reconcile passes that failed, by resource kind.",
		}, []string{"kind"}),
		watchRestarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: Prefix + "watch_restarts_total",
			Help: "Watches that closed and opened again, by resource kind.",
		}, []string{"kind"}),
	}

	// Layer 1. The runtime collectors report the process itself: its
	// memory, its goroutines, its open files, and its start time. The
	// build info gauge always holds 1, and its labels carry the fact,
	// which is the Prometheus convention for a string. One panel that
	// reads liken_build_info across a cluster shows every component's
	// release, because every liken process publishes this one name.
	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: Prefix + "build_info",
		Help: "The release each liken component runs, as labels on a gauge that is always 1.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(component, version).Set(1)
	o.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		o.reconcileDuration,
		o.reconcileErrors,
		o.watchRestarts,
	)

	for _, kind := range reconciles {
		o.reconcileDuration.WithLabelValues(kind)
		o.reconcileErrors.WithLabelValues(kind)
	}
	for _, kind := range watches {
		o.watchRestarts.WithLabelValues(kind)
	}
	return o
}

// Registry is where a program registers its layer 3. One registry
// serves one process, so the domain metrics and the two layers above
// them answer the same scrape.
func (o *Operator) Registry() *prometheus.Registry {
	return o.registry
}

// ObserveReconcile records one finished reconcile pass. Every pass
// counts, including a pass that changed nothing, because the rate of
// passes is what says the loop still runs. A pass that returned an
// error counts twice: once in the histogram, because it still took
// time, and once in the error counter.
func (o *Operator) ObserveReconcile(kind string, took time.Duration, err error) {
	o.reconcileDuration.WithLabelValues(kind).Observe(took.Seconds())
	if err != nil {
		o.reconcileErrors.WithLabelValues(kind).Inc()
	}
}

// WatchRestarted records one watch that the operator opened again. A
// Kubernetes API server ends a watch on its own schedule, so a low
// rate here is normal. A high rate says the stream breaks faster
// than the loop can use it.
func (o *Operator) WatchRestarted(kind string) {
	o.watchRestarts.WithLabelValues(kind).Inc()
}
