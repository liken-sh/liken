package main

// The API's own registry.
//
// The route label is the RFC 6570 template, never the concrete path,
// so no endpoint name enters Prometheus. The same string is the route
// field of the one log line each request writes.
//
// These four series are the API's. The capture counters are the
// container's, and nothing double-counts.

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// apiMetrics holds the registry the API's metrics listener serves.
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
			Help: "Requests this API answered, by route template, method, and status.",
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

// answered counts one request and observes how long it took to its
// headers, which is the time a caller waits before anything arrives.
func (m *apiMetrics) answered(route, method string, status int, headers time.Duration) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(route, method, statusLabel(status)).Inc()
	m.duration.WithLabelValues(route).Observe(headers.Seconds())
}

// streaming moves the gauge of the streams this API relays now.
func (m *apiMetrics) streaming(aspect string, delta float64) {
	if m == nil || aspect == "" {
		return
	}
	m.streams.WithLabelValues(aspect).Add(delta)
}

// certificateExpiry reports when the first of this API's certificates
// runs out. The timestamp is the certificate's own, so a reader
// computes the remaining life itself.
func (m *apiMetrics) certificateExpiry(at time.Time) {
	if m == nil || at.IsZero() {
		return
	}
	m.expiry.Set(float64(at.Unix()))
}

// statusLabel is the status as a label: the number itself, so a panel
// groups by the exact code a caller saw.
func statusLabel(status int) string {
	return strconv.Itoa(status)
}

// apiRegistryHandler serves the API's registry, and the two probes
// beside it, on the metrics port. These three are on the plain
// metrics port and not on the TLS port the API serves, because a
// scrape and the kubelet hold no token and no CA, and none of the
// three carries anything a caller asked for.
func apiRegistryHandler(m *apiMetrics, ready func() bool) http.Handler {
	served := http.NewServeMux()
	served.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	served.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writePlain(w, http.StatusOK, "ok")
	})
	served.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready() {
			writePlain(w, http.StatusServiceUnavailable, "the API holds no certificate yet")
			return
		}
		writePlain(w, http.StatusOK, "ok")
	})
	return served
}
