package main

// This file holds the series display-api serves: requests, time to
// headers, streams in flight, and the certificate's expiry. The
// capture counters, bytes, seconds, frames, and failures, are the
// sidecar's alone, so nothing here double counts what the node
// already counted.

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// The API's four series. The route label is the RFC 6570 template
// and never the path a caller sent, so the label set is bounded by
// the route table and not by the names in the cluster.
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
			Help: "Requests this API answered, by route template, method and status.",
		}, []string{"route", "method", "status"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "display_api_request_seconds",
			Help: "How long one request took to its response headers.",
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
	// The one aspect this API streams starts at zero, so a panel
	// reads a screen nobody is watching rather than no series.
	m.streams.WithLabelValues(screenAspect).Set(0)
	return m
}

// Every recording method is safe with no registry behind it, the way
// this repository's other metrics are, so a test drives a request
// with no listener.
func (m *apiMetrics) answered(route, method string, status int, headerTime time.Duration) {
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

// The status label is the code itself, a set of bounded size, and
// never the status phrase.
func statusLabel(status int) string {
	return strconv.Itoa(status)
}

// The three paths every liken process serves beside its own work:
// /metrics, /healthz, and /readyz. None of them takes a token, so
// the kubelet and the scrape need none.
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
