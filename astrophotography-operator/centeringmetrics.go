package main

// The series of centering (plan 08): how many rounds each centering
// took, and the last error of each target. The runner sets them when a
// round ends, so a scrape reads them from memory.

import "github.com/prometheus/client_golang/prometheus"

type centeringMetrics struct {
	rounds    prometheus.Histogram
	lastError *prometheus.GaugeVec
}

func newCenteringMetrics(registry *prometheus.Registry) *centeringMetrics {
	m := &centeringMetrics{
		// spec.centering.attempts is at most 20, and plan 02 expects two
		// or three rounds.
		rounds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    metricsPrefix + "centering_rounds",
			Help:    "Rounds of solving and slewing that one centering took, whether it ended within the tolerance or gave up.",
			Buckets: []float64{1, 2, 3, 5, 10, 20},
		}),
		lastError: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: metricsPrefix + "centering_error_arcseconds",
			Help: "The angle between the last solved center and the target's coordinates, in arcseconds, by target.",
		}, []string{"namespace", "target"}),
	}
	registry.MustRegister(m.rounds, m.lastError)
	return m
}
