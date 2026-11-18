package main

// The route label is the RFC 6570 route template and not the request
// path, so endpoint names do not become label values. The log line of
// each request writes the same template in its route field.
//
// This registry holds only the API container's series. The capture
// containers serve their own audio_capture_* series from
// capturemetrics.go, so a tap is not counted twice.

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// apiMetrics is the registry that the API's metrics listener serves.
type apiMetrics struct {
	registry *prometheus.Registry

	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	streams  *prometheus.GaugeVec
	expiry   prometheus.Gauge
}

func newAPIMetrics(version string) *apiMetrics {
	m := &apiMetrics{
		registry: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "audio_api_requests_total",
			Help: "Requests this API served, by route template, method, and status.",
		}, []string{"route", "method", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "audio_api_request_seconds",
			Help: "How long one request took to its headers, by route template.",
		}, []string{"route"}),
		streams: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "audio_api_streams_active",
			Help: "Capture streams this API is relaying now.",
		}, []string{"aspect"}),
		expiry: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "audio_api_certificate_expiry_seconds",
			Help: "When the first of this API's certificates runs out, in seconds since the epoch.",
		}),
	}

	buildInfo := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "liken_build_info",
		Help: "The release this process was built from. Always 1.",
	}, []string{"component", "version"})
	buildInfo.WithLabelValues(apiComponent, version).Set(1)

	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo,
		m.requests, m.duration, m.streams, m.expiry,
	)
	return m
}

// observeRequest counts a finished request and records the time to its
// response headers, which is how long the caller waits before any data
// arrives.
func (m *apiMetrics) observeRequest(route, method string, status int, headers time.Duration) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(route, method, statusLabel(status)).Inc()
	m.duration.WithLabelValues(route).Observe(headers.Seconds())
}

// streaming adds delta to the gauge of capture streams that the API is
// relaying for this aspect.
func (m *apiMetrics) streaming(aspect string, delta float64) {
	if m == nil || aspect == "" {
		return
	}
	m.streams.WithLabelValues(aspect).Add(delta)
}

// certificateExpiry sets the gauge to the time when the first of the
// API's certificates expires. The gauge holds a timestamp, so a
// dashboard computes the time left.
func (m *apiMetrics) certificateExpiry(at time.Time) {
	if m == nil || at.IsZero() {
		return
	}
	m.expiry.Set(float64(at.Unix()))
}

// statusLabel keeps the exact status code and not its class, so a
// panel can group by each code.
func statusLabel(status int) string {
	return strconv.Itoa(status)
}

// apiRegistryHandler serves /metrics, /healthz, and /readyz on the
// plain metrics port and not the API's TLS port, because Prometheus and
// the kubelet have no token and no CA. None of these paths returns
// capture data.
func apiRegistryHandler(m *apiMetrics, ready func() bool) http.Handler {
	served := http.NewServeMux()
	served.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	served.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writePlain(w, http.StatusOK, "ok")
	})
	served.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready() {
			writePlain(w, http.StatusServiceUnavailable, "the API has no certificate yet")
			return
		}
		writePlain(w, http.StatusOK, "ok")
	})
	return served
}
