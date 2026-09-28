package main

// This file holds the sidecar's own series. They are counted on the
// node where the bytes are made, so the API counts requests and the
// node counts frames, bytes, seconds, and failures, and nothing is
// counted twice.

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// The four reasons a capture fails: the compositor denied it, the
// output was busy, the compositor could not be reached, or the
// encoder failed. The label is bounded to these four so an alert can
// name each one and a series count never grows with the error text.
const (
	deniedReason     = "denied"
	busyReason       = "busy"
	compositorReason = "compositor"
	encoderReason    = "encoder"
)

type captureMetrics struct {
	registry *prometheus.Registry
	readyNow prometheus.Gauge
	graph    *prometheus.GaugeVec
	bytes    *prometheus.CounterVec
	seconds  *prometheus.CounterVec
	active   *prometheus.GaugeVec
	frames   *prometheus.CounterVec
	failures *prometheus.CounterVec
}

func newCaptureMetrics(component, version string) *captureMetrics {
	registry := newProcessRegistry(component, version)
	m := &captureMetrics{
		registry: registry,
		readyNow: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "display_capture_ready",
			Help: "1 while this sidecar holds the certificate the API verifies it under, 0 while it does not.",
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
			Help: "Frames this sidecar took from the compositor, by format.",
		}, []string{"format"}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "display_capture_failures_total",
			Help: "Captures that failed, by why.",
		}, []string{"reason"}),
	}
	registry.MustRegister(m.readyNow, m.graph, m.bytes, m.seconds, m.active, m.frames, m.failures)

	// A counter a scrape has never seen is a counter an alert cannot
	// rate, so every bounded label this process can write starts at
	// zero.
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

// Every recording here is safe with no registry behind it, which is
// how the tests drive a capture.
func (m *captureMetrics) ready(held bool) {
	if m == nil {
		return
	}
	if held {
		m.readyNow.Set(1)
		return
	}
	m.readyNow.Set(0)
}

// Which graph this node converts with, as a pair of gauges: the one
// it runs reads 1 and the other reads 0, so a fleet panel counts the
// nodes that fell back to the CPU without reading a log.
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

func (m *captureMetrics) failed(reason string) {
	if m == nil {
		return
	}
	m.failures.WithLabelValues(reason).Inc()
}
