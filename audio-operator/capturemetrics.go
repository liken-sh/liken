package main

// Only the capture container exports these series, and the API
// container exports its own request metrics, so a tap is not counted
// twice.
//
// /metrics, /healthz, and /readyz need no token, because Prometheus and
// the kubelet send none. They share the TLS listener of the taps, whose
// certificate is signed by a CA that Prometheus does not trust, so the
// PodMonitor skips certificate verification for this port.

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// captureComponent is this process's name on liken_build_info.
const captureComponent = "audio-capture"

// Every call to failed passes one of these reasons, so the reason label
// of audio_capture_failures_total has a fixed set of values and the
// dashboard's panel by reason covers every failure.
const (
	failureConnect     = "connect"
	failureTarget      = "target"
	failureWrongTarget = "wrong-target"
	failureEncoder     = "encoder"
	failureLimit       = "limit"
	failureCertificate = "certificate"
)

// Each method of captureMetrics does nothing on a nil receiver.
// captureRegistryHandler needs a non-nil one.
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
			Help: "One while the container has a server certificate and can serve a tap.",
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
			Help: "Taps that failed, by the kind of failure.",
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

// readiness sets audio_capture_ready to 1 when the container has the
// server certificate that audio-api signed, because without it no tap
// can complete a TLS handshake.
func (m *captureMetrics) readiness(hasCertificate bool) {
	if m == nil {
		return
	}
	m.ready.Set(gauge(hasCertificate))
}

func (m *captureMetrics) started(aspect string) {
	if m == nil {
		return
	}
	m.active.WithLabelValues(aspect).Inc()
}

func (m *captureMetrics) finished(aspect, format string, sent int64, ran time.Duration) {
	if m == nil {
		return
	}
	m.active.WithLabelValues(aspect).Dec()
	m.bytes.WithLabelValues(aspect, format).Add(float64(sent))
	m.seconds.WithLabelValues(aspect, format).Add(ran.Seconds())
}

func (m *captureMetrics) failed(reason string) {
	if m == nil {
		return
	}
	m.failures.WithLabelValues(reason).Inc()
}

func captureRegistryHandler(m *captureMetrics) http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}
