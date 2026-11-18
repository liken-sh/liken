package main

import "github.com/prometheus/client_golang/prometheus"

type runnerMetrics struct {
	framesExposed    *prometheus.CounterVec
	exposureFailures *prometheus.CounterVec
	preparation      prometheus.Histogram
}

func newRunnerMetrics(registry *prometheus.Registry) *runnerMetrics {
	m := &runnerMetrics{
		framesExposed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "frames_exposed_total",
			Help: "Frames that the driver saved in the outbox and the runner counted as pending, by target.",
		}, []string{"namespace", "target"}),
		exposureFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "exposure_failures_total",
			Help: "Exposures that saved no frame because the driver reported Alert, did not finish in time, or saved the frame elsewhere, by target.",
		}, []string{"namespace", "target"}),
		// Writing the keywords takes milliseconds and turning the filter
		// wheel takes seconds, so the buckets run from 10 ms to 164 s.
		preparation: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    metricsPrefix + "frame_preparation_seconds",
			Help:    "How long the settings before one exposure take: the upload, the frame type, the filter, and the keywords.",
			Buckets: prometheus.ExponentialBuckets(0.01, 4, 8),
		}),
	}
	registry.MustRegister(m.framesExposed, m.exposureFailures, m.preparation)
	return m
}
