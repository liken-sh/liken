package main

// The capture container's own registry.
//
// The counters here are emitted by the container only, so nothing
// double-counts: the API keeps its own request series, and the bytes
// and the stream time are the container's.
//
// /metrics, /healthz, and /readyz need no token, because the scrape
// and the kubelet hold none. They share the one TLS listener the taps
// use, with a leaf Prometheus has no anchor for, which is why the
// PodMonitor endpoint skips verification.

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// captureComponent is this process's name on liken_build_info.
const captureComponent = "audio-capture"

// The fixed vocabulary audio_capture_failures_total takes. Every
// failure a tap can have is counted under one of these six names, so
// the dashboard's panel by reason is complete and no label arrives
// that nobody chose.
const (
	failureConnect     = "connect"
	failureTarget      = "target"
	failureWrongTarget = "wrong-target"
	failureEncoder     = "encoder"
	failureLimit       = "limit"
	failureCertificate = "certificate"
)

// captureMetrics holds the registry the capture listener serves.
type captureMetrics struct {
	registry *prometheus.Registry

	ready    prometheus.Gauge
	bytes    *prometheus.CounterVec
	seconds  *prometheus.CounterVec
	active   *prometheus.GaugeVec
	failures *prometheus.CounterVec
}

func newCaptureMetrics(version string) *captureMetrics {
	m := &captureMetrics{
		registry: prometheus.NewRegistry(),
		ready: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "audio_capture_ready",
			Help: "One while the container holds a server certificate and can answer a tap.",
		}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "audio_capture_bytes_total",
			Help: "Bytes this container has sent as capture bodies.",
		}, []string{"aspect", "format"}),
		seconds: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "audio_capture_seconds_total",
			Help: "Seconds this container has held capture streams open.",
		}, []string{"aspect", "format"}),
		active: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "audio_captures_active",
			Help: "Capture streams running on this node now.",
		}, []string{"aspect"}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "audio_capture_failures_total",
			Help: "Taps that failed, by the failure's own kind.",
		}, []string{"reason"}),
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "The release this process was built from. Always 1.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(captureComponent, version).Set(1)

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		m.ready, m.bytes, m.seconds, m.active, m.failures,
	)
	return m
}

// readiness reports whether the container holds the leaf the API
// signed. Until that file exists no tap can complete its handshake,
// and this gauge is zero.
func (m *captureMetrics) readiness(held bool) {
	if m == nil {
		return
	}
	m.ready.Set(gauge(held))
}

// started counts one tap onto the active gauge.
func (m *captureMetrics) started(aspect string) {
	if m == nil {
		return
	}
	m.active.WithLabelValues(aspect).Inc()
}

// finished records one tap that ended: how long it ran and how many
// bytes it delivered.
func (m *captureMetrics) finished(aspect, format string, sent int64, ran time.Duration) {
	if m == nil {
		return
	}
	m.active.WithLabelValues(aspect).Dec()
	m.bytes.WithLabelValues(aspect, format).Add(float64(sent))
	m.seconds.WithLabelValues(aspect, format).Add(ran.Seconds())
}

// failed counts one tap that did not deliver, under the reason's own
// name.
func (m *captureMetrics) failed(reason string) {
	if m == nil {
		return
	}
	m.failures.WithLabelValues(reason).Inc()
}

// captureRegistryHandler serves this container's registry at /metrics.
func captureRegistryHandler(m *captureMetrics) http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
