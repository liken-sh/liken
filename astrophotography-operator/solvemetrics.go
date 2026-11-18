package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// The values of the result label.
const (
	solveSolved   = "solved"
	solveUnsolved = "unsolved"
	solveFailed   = "failed"
)

// solveBuckets run from 0.25 s to 256 s. A field that does not solve
// runs until the solver's two-minute timeout, plus any time it waited
// in the solver's queue.
var solveBuckets = prometheus.ExponentialBuckets(0.25, 2, 11)

type solveMetrics struct {
	solves   *prometheus.CounterVec
	duration prometheus.Histogram
}

func newSolveMetrics(registry *prometheus.Registry) *solveMetrics {
	m := &solveMetrics{
		solves: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "solves_total",
			Help: "Attempts to solve a frame, by their outcome: solved, unsolved, or failed.",
		}, []string{"result"}),
		duration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    metricsPrefix + "solve_duration_seconds",
			Help:    "How long a solver took to solve one star list, including the wait in its queue.",
			Buckets: solveBuckets,
		}),
	}
	registry.MustRegister(m.solves, m.duration)
	for _, result := range []string{solveSolved, solveUnsolved, solveFailed} {
		m.solves.WithLabelValues(result)
	}
	return m
}

// observeSolve counts one attempt. A failed request has no solver
// time, so it adds no duration sample.
func (m *metrics) observeSolve(result string, took time.Duration) {
	m.solve.solves.WithLabelValues(result).Inc()
	if result != solveFailed {
		m.solve.duration.Observe(took.Seconds())
	}
}
