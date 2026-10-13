package main

// The runner's series of layer 3 (root plan 65): the frames it counted,
// the exposures that gave no frame, and the time it spends on the
// settings before each exposure. A scrape reads them from memory; the
// runner sets each one when the fact happens.

import "github.com/prometheus/client_golang/prometheus"

type runnerMetrics struct {
	framesExposed    *prometheus.CounterVec
	exposureFailures *prometheus.CounterVec
	preparation      prometheus.Histogram
}

// newRunnerMetrics builds the runner's series and registers them.
func newRunnerMetrics(registry *prometheus.Registry) *runnerMetrics {
	m := &runnerMetrics{
		framesExposed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "frames_exposed_total",
			Help: "Frames that the driver saved in the outbox and the runner counted as pending, by target.",
		}, []string{"namespace", "target"}),
		exposureFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "exposure_failures_total",
			Help: "Exposures that gave no frame: the driver answered Alert, did not finish in time, or saved the frame elsewhere, by target.",
		}, []string{"namespace", "target"}),
		// Plan 20's drill measured 2 to 43 ms for each settled keyword,
		// so a frame's settings take tens of milliseconds, and a turn of
		// the filter wheel takes seconds.
		preparation: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    metricsPrefix + "frame_preparation_seconds",
			Help:    "How long the settings before one exposure take: the upload, the frame type, the filter, and the keywords.",
			Buckets: prometheus.ExponentialBuckets(0.01, 4, 8),
		}),
	}
	registry.MustRegister(m.framesExposed, m.exposureFailures, m.preparation)
	return m
}
