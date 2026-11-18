package main

// The metrics for guiding and dithering: the dithers that settled, the
// failures to guide, and how long PHD2 takes to settle. They show a
// guiding problem before it shows as fewer frames in the archive.

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
		// The buckets double from 1 second to 512 seconds, which covers a
		// settle with a calibration before it.
		settle: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    metricsPrefix + "settle_seconds",
			Help:    "How long a guide or a dither takes from the request to SettleDone, calibration included, by operation.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		}, []string{"operation"}),
	}
	registry.MustRegister(m.dithers, m.failures, m.settle)
	return m
}
