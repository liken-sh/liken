package main

// The metrics of the api role. The route label is the route template
// and not the request path, so namespaces and Player names never become
// label values. The api role does not count captured bytes or frames,
// because the sidecars that it reads from already count them.

import (
	"math"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// apiBuildInfoComponent is the component label of liken_build_info.
const apiBuildInfoComponent = "media-api"

// The values of the upstream label: the display or audio API that the
// api role sent a request to.
const (
	upstreamDisplay = "display"
	upstreamAudio   = "audio"
)

type apiMetrics struct {
	baseMetrics

	requests          *prometheus.CounterVec
	requestSeconds    *prometheus.HistogramVec
	streamsActive     *prometheus.GaugeVec
	upstreamRequests  *prometheus.CounterVec
	composeOffset     prometheus.Histogram
	composeOffsetLast prometheus.Gauge
	certificateExpiry prometheus.Gauge
}

func newAPIMetrics(version string) *apiMetrics {
	base := newBaseMetrics(apiBuildInfoComponent, version)
	m := &apiMetrics{
		baseMetrics: base,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "media_api_requests_total",
			Help: "Requests this API responded to, by route template, method, and status.",
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
			Help: "Requests this API made to the API of another operator, by that API and status.",
		}, []string{"upstream", "status"}),
		composeOffset: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "media_api_compose_offset_seconds",
			Help: "How far apart the two upstreams' header instants were, without a sign.",
			// 0.033 s is one frame at 30 frames per second.
			Buckets: []float64{0.001, 0.005, 0.01, 0.033, 0.1, 0.25, 0.5, 1, 2},
		}),
		composeOffsetLast: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "media_api_compose_offset_last_seconds",
			Help: "The last correction, signed: a negative value means the audio headers arrived first.",
		}),
		certificateExpiry: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "media_api_certificate_expiry_seconds",
			Help: "Seconds until the serving certificate expires.",
		}),
	}
	base.registry.MustRegister(
		m.requests,
		m.requestSeconds,
		m.streamsActive,
		m.upstreamRequests,
		m.composeOffset,
		m.composeOffsetLast,
		m.certificateExpiry,
	)
	return m
}

// The methods of apiMetrics do nothing on a nil receiver, so a test
// that does not need metrics can pass nil.
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

// observeOffset records the offset of one stream of a composed capture:
// the time its response headers arrived minus the time the reference
// stream's headers arrived. The histogram's buckets are all positive,
// so it records the absolute value, and the gauge records the sign.
func (m *apiMetrics) observeOffset(seconds float64) {
	if m == nil {
		return
	}
	m.composeOffset.Observe(math.Abs(seconds))
	m.composeOffsetLast.Set(seconds)
}

func (m *apiMetrics) observeExpiry(seconds float64) {
	if m == nil {
		return
	}
	m.certificateExpiry.Set(seconds)
}

// trackStream increments the active streams gauge and returns the
// function that decrements it, so the caller can defer the decrement
// and no return path leaves an ended stream counted.
func (m *apiMetrics) trackStream(aspect string) func() {
	if m == nil {
		return func() {}
	}
	m.streamsActive.WithLabelValues(aspect).Inc()
	return func() { m.streamsActive.WithLabelValues(aspect).Dec() }
}
