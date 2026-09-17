package main

// This file holds the api role's own series, on the metrics base every
// role shares. The route label is the RFC 6570 template and never the
// concrete path, so no namespace or Player name enters a label and the
// series count is bounded by the route table. No capture counter is
// here: the sidecars count the bytes and frames they capture, this API
// has no sidecar, and a counter here would double-count every
// composition.

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// apiBuildInfoComponent is the api role's name in liken_build_info,
// beside the operator's.
const apiBuildInfoComponent = "media-api"

// The two values the upstream label takes, one per sibling API.
const (
	upstreamDisplay = "display"
	upstreamAudio   = "audio"
)

// apiMetrics is the api role's series: requests by route, method, and
// status; the time to the first response header by route; the streams
// being written now by aspect; upstream requests by sibling and
// status; the measured header offset of each composition; and the
// seconds the serving leaf has left. While nothing streams, a scrape
// reads the build info, the certificate expiry, and the counters as
// they stand; the gauge reads zero and the histograms grow only with
// a request.
type apiMetrics struct {
	baseMetrics

	requests          *prometheus.CounterVec
	requestSeconds    *prometheus.HistogramVec
	streamsActive     *prometheus.GaugeVec
	upstreamRequests  *prometheus.CounterVec
	composeOffset     prometheus.Histogram
	certificateExpiry prometheus.Gauge
}

func newAPIMetrics(version string) *apiMetrics {
	base := newBaseMetrics(apiBuildInfoComponent, version)
	m := &apiMetrics{
		baseMetrics: base,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "media_api_requests_total",
			Help: "Requests this API answered, by route template, method, and status.",
		}, []string{"route", "method", "status"}),
		requestSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "media_api_request_seconds",
			Help: "How long one request took to its first response header, by route template.",
		}, []string{"route"}),
		streamsActive: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "media_api_streams_active",
			Help: "Streams this API is writing now, by aspect.",
		}, []string{"aspect"}),
		upstreamRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "media_api_upstream_requests_total",
			Help: "Requests this API made to a sibling API, by sibling and status.",
		}, []string{"upstream", "status"}),
		composeOffset: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "media_api_compose_offset_seconds",
			Help: "The measured difference between the two upstreams' header instants.",
		}),
		certificateExpiry: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "media_api_certificate_expiry_seconds",
			Help: "The seconds the serving leaf has left.",
		}),
	}
	base.registry.MustRegister(
		m.requests,
		m.requestSeconds,
		m.streamsActive,
		m.upstreamRequests,
		m.composeOffset,
		m.certificateExpiry,
	)
	return m
}

// Every observer answers on a nil receiver, so a test that has no use
// for a registry builds none and the code under test still runs.
func (m *apiMetrics) observeRequest(route, method string, status int, header time.Duration) {
	if m == nil {
		return
	}
	m.requests.WithLabelValues(route, method, strconv.Itoa(status)).Inc()
	m.requestSeconds.WithLabelValues(route).Observe(header.Seconds())
}

func (m *apiMetrics) observeUpstream(upstream, status string) {
	if m == nil {
		return
	}
	m.upstreamRequests.WithLabelValues(upstream, status).Inc()
}

func (m *apiMetrics) observeOffset(seconds float64) {
	if m == nil {
		return
	}
	m.composeOffset.Observe(seconds)
}

func (m *apiMetrics) observeExpiry(seconds float64) {
	if m == nil {
		return
	}
	m.certificateExpiry.Set(seconds)
}

// holdStream raises the gauge and answers the closure that lowers it,
// for the caller to defer. One closure cannot be forgotten on an error
// path the way a second call can, and a gauge that is raised and never
// lowered would report a stream that ended.
func (m *apiMetrics) holdStream(aspect string) func() {
	if m == nil {
		return func() {}
	}
	m.streamsActive.WithLabelValues(aspect).Inc()
	return func() { m.streamsActive.WithLabelValues(aspect).Dec() }
}
