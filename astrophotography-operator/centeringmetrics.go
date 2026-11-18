package main

import "github.com/prometheus/client_golang/prometheus"

type centeringMetrics struct {
	rounds    prometheus.Histogram
	lastError *prometheus.GaugeVec
}

func newCenteringMetrics(registry *prometheus.Registry) *centeringMetrics {
	m := &centeringMetrics{
		// The Target CRD limits spec.centering.attempts to 20.
		rounds: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    metricsPrefix + "centering_rounds",
			Help:    "Rounds of solving and slewing that one centering took, whether it ended within the tolerance, gave up, or failed after a round solved.",
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
