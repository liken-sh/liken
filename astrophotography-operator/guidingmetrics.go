package main

// The series of guiding and dithering (root plan 65, layer 3): the
// dithers that settled, the failures to guide, and how long PHD2 takes
// to settle. A night whose frames wait on the guiding shows it here
// before it shows in the frames.

import "github.com/prometheus/client_golang/prometheus"

type guidingMetrics struct {
	dithers  *prometheus.CounterVec
	failures *prometheus.CounterVec
	settle   *prometheus.HistogramVec
}

func newGuidingMetrics(registry *prometheus.Registry) *guidingMetrics {
	m := &guidingMetrics{
		dithers: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "dithers_total",
			Help: "Dithers that PHD2 settled after, by target.",
		}, []string{"namespace", "target"}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "guiding_failures_total",
			Help: "Attempts to guide or dither that failed, such as a settle that timed out or no star found, by target.",
		}, []string{"namespace", "target"}),
		// Plan 09's drill settled a guide in 10 seconds after a
		// calibration of 73, so the buckets run from a second to over
		// eight minutes.
		settle: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    metricsPrefix + "settle_seconds",
			Help:    "How long a guide or a dither takes from the request to SettleDone, calibration included, by operation.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		}, []string{"operation"}),
	}
	registry.MustRegister(m.dithers, m.failures, m.settle)
	return m
}
