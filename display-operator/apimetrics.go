package main

// This file defines the metrics of display-api. display-api forwards
// each capture to the capture sidecar on the Display's node, and only
// the sidecar counts the bytes, seconds, frames, and failures of a
// capture, so a capture is not counted twice.

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// apiMetrics holds the metrics of display-api. The route label is the
// route's template, such as /v1/display/displays/{name}/screen.png,
// and never the request path, so the number of series does not grow
// with the number of Displays.
type apiMetrics struct {
	registry *prometheus.Registry
	requests *prometheus.CounterVec
	latency  *prometheus.HistogramVec
	streams  *prometheus.GaugeVec
	expiry   prometheus.Gauge
}

func newAPIMetrics(component, version string) *apiMetrics {
	registry := newProcessRegistry(component, version)
	m := &apiMetrics{
		registry: registry,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "display_api_requests_total",
			Help: "Requests this API responded to, by route template, method, and status.",
		}, []string{"route", "method", "status"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "display_api_request_seconds",
			Help: "Seconds from the start of a request to its response headers.",
		}, []string{"route"}),
		streams: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "display_api_streams_active",
			Help: "Capture responses this API is streaming right now.",
		}, []string{"aspect"}),
		expiry: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "display_api_certificate_expiry_seconds",
			Help: "When the serving certificate expires, in seconds since the epoch.",
		}),
	}
	registry.MustRegister(m.requests, m.latency, m.streams, m.expiry)
	// Setting the gauge creates the series before the first stream, so
	// a dashboard shows 0 instead of no data.
	m.streams.WithLabelValues(screenAspect).Set(0)
	return m
}

// recordRequest records one finished request. Every recording method does
// nothing on a nil *apiMetrics, so a test can serve a request without
// metrics.
func (m *apiMetrics) recordRequest(route, method string, status int, headerTime time.Duration) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(route, method, statusLabel(status)).Inc()
	m.latency.WithLabelValues(route).Observe(headerTime.Seconds())
}

func (m *apiMetrics) streaming(aspect string, delta float64) {
	if m == nil {
		return
	}
	m.streams.WithLabelValues(aspect).Add(delta)
}

func (m *apiMetrics) certificateExpires(at time.Time) {
	if m == nil {
		return
	}
	m.expiry.Set(float64(at.Unix()))
}

func statusLabel(status int) string {
	return strconv.Itoa(status)
}

// processHandler serves /metrics, /healthz, and /readyz. None of them
// requires a token, because the kubelet's probes and the Prometheus
// scrape send none. /readyz returns 503 while ready returns false.
func processHandler(registry *prometheus.Registry, ready func() bool) *http.ServeMux {
	served := http.NewServeMux()
	served.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	served.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	served.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if ready != nil && !ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready\n"))
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	return served
}
