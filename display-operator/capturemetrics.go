package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// These are the values of the reason label of
// display_capture_failures_total. denied is a capture that weston
// refused, busy is an output that already runs its limit of captures,
// compositor is any other compositor error, such as a missing socket
// or a lost connection, and encoder is an ffmpeg failure. The label
// never holds error text, so the number of series stays fixed and an
// alert can match each reason.
const (
	deniedReason     = "denied"
	busyReason       = "busy"
	compositorReason = "compositor"
	encoderReason    = "encoder"
)

// captureMetrics holds the metrics of the capture sidecar, which takes
// frames from the compositor on its node and encodes them.
type captureMetrics struct {
	registry  *prometheus.Registry
	readiness prometheus.Gauge
	graph     *prometheus.GaugeVec
	bytes     *prometheus.CounterVec
	seconds   *prometheus.CounterVec
	active    *prometheus.GaugeVec
	frames    *prometheus.CounterVec
	failures  *prometheus.CounterVec
}

func newCaptureMetrics(component, version string) *captureMetrics {
	registry := newProcessRegistry(component, version)
	m := &captureMetrics{
		registry: registry,
		readiness: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "display_capture_ready",
			Help: "1 while this sidecar has a certificate that the API can verify, 0 while it does not.",
		}),
		graph: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "display_capture_conversion",
			Help: "1 on the graph this node converts frames with, 0 on the other.",
		}, []string{"graph"}),
		bytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "display_capture_bytes_total",
			Help: "Bytes this sidecar encoded and sent, by aspect and format.",
		}, []string{"aspect", "format"}),
		seconds: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "display_capture_seconds_total",
			Help: "Seconds this sidecar spent streaming, by aspect and format.",
		}, []string{"aspect", "format"}),
		active: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "display_captures_active",
			Help: "Captures running on this node right now.",
		}, []string{"aspect"}),
		frames: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "display_capture_frames_total",
			Help: "Frames this sidecar received from the compositor, by format.",
		}, []string{"format"}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "display_capture_failures_total",
			Help: "Captures that failed, by reason.",
		}, []string{"reason"}),
	}
	registry.MustRegister(m.readiness, m.graph, m.bytes, m.seconds, m.active, m.frames, m.failures)

	// rate() and increase() cannot count the first value of a series
	// that appears above zero, so an alert would miss the first
	// failures. So every series this process can write starts at 0.
	m.active.WithLabelValues(screenAspect).Set(0)
	m.graph.WithLabelValues(vaapiGraph).Set(0)
	m.graph.WithLabelValues(softwareGraph).Set(0)
	for _, reason := range []string{deniedReason, busyReason, compositorReason, encoderReason} {
		m.failures.WithLabelValues(reason).Add(0)
	}
	for _, form := range screenForms {
		m.bytes.WithLabelValues(screenAspect, form.mediaType).Add(0)
		m.seconds.WithLabelValues(screenAspect, form.mediaType).Add(0)
		m.frames.WithLabelValues(form.mediaType).Add(0)
	}
	return m
}

// ready sets display_capture_ready to 1 while the sidecar serves the
// certificate that display-api issued, and to 0 while it serves its
// own self-signed one. Every recording method does nothing on a nil
// *captureMetrics, so a test can run a capture without metrics.
func (m *captureMetrics) ready(held bool) {
	if m == nil {
		return
	}
	if held {
		m.readiness.Set(1)
		return
	}
	m.readiness.Set(0)
}

// converting sets the gauge of the ffmpeg graph the node uses to 1
// and the other to 0, so a dashboard can count the nodes that fell
// back to software conversion.
func (m *captureMetrics) converting(software bool) {
	if m == nil {
		return
	}
	chosen, other := vaapiGraph, softwareGraph
	if software {
		chosen, other = softwareGraph, vaapiGraph
	}
	m.graph.WithLabelValues(chosen).Set(1)
	m.graph.WithLabelValues(other).Set(0)
}

func (m *captureMetrics) wrote(aspect, format string, count int) {
	if m == nil {
		return
	}
	m.bytes.WithLabelValues(aspect, format).Add(float64(count))
}

func (m *captureMetrics) streamed(aspect, format string, over time.Duration) {
	if m == nil {
		return
	}
	m.seconds.WithLabelValues(aspect, format).Add(over.Seconds())
}

func (m *captureMetrics) capturing(aspect string, delta float64) {
	if m == nil {
		return
	}
	m.active.WithLabelValues(aspect).Add(delta)
}

func (m *captureMetrics) framed(format string) {
	if m == nil {
		return
	}
	m.frames.WithLabelValues(format).Inc()
}

// failed counts one failed capture. reason is one of the reason
// constants above.
func (m *captureMetrics) failed(reason string) {
	if m == nil {
		return
	}
	m.failures.WithLabelValues(reason).Inc()
}
