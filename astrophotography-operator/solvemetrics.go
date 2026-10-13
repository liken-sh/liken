package main

// The series of plate solves (plan 08): how many solves each Solver
// answered, by their outcome, and how long a solver took to answer.
// The operator sets them when a solve returns, so a scrape reads them
// from memory.

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// The outcomes of one request to a solver.
const (
	solveSolved   = "solved"
	solveUnsolved = "unsolved"
	solveFailed   = "failed"
)

// solveBuckets cover a solve: about 1 s for a field that solves, and
// up to the solver's wall time of two minutes, after a wait in its
// queue, for one that does not.
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
			Help:    "How long a solver took to answer one star list, with the wait in its queue.",
			Buckets: solveBuckets,
		}),
	}
	registry.MustRegister(m.solves, m.duration)
	for _, result := range []string{solveSolved, solveUnsolved, solveFailed} {
		m.solves.WithLabelValues(result)
	}
	return m
}

// observeSolve counts one attempt, and records the solver's time for an
// attempt that the solver answered.
func (m *metrics) observeSolve(result string, took time.Duration) {
	m.solve.solves.WithLabelValues(result).Inc()
	if result != solveFailed {
		m.solve.duration.Observe(took.Seconds())
	}
}
