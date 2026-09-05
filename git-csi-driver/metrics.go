package main

// metrics.go holds the gauges the node plugin exports and the listener
// that serves them. Every fact the driver reports reaches the
// condition, the Events, and these numbers.

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsDeadline bounds a request's headers and the listener's stop.
const metricsDeadline = 30 * time.Second

// metrics is the registry the listener serves and the two gauges plan
// 04 fills. Plan 05 adds the rest of the design's list.
type metrics struct {
	registry *prometheus.Registry
	armed    *prometheus.GaugeVec
	pending  *prometheus.GaugeVec
}

// metricLabels name the claim a person would look up.
var metricLabels = []string{"namespace", "claim"}

func newMetrics() *metrics {
	readings := &metrics{
		registry: prometheus.NewRegistry(),
		// One when a class of this driver arms the volume, zero when none
		// does.
		armed: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_armed", Help: "One when a class of the driver arms the volume, zero when none does."}, metricLabels),
		// How many paths the last scan found that the driver has not
		// committed.
		pending: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_pending_paths", Help: "Paths the last scan found that the driver has not committed."}, metricLabels),
	}
	readings.registry.MustRegister(readings.armed, readings.pending)
	return readings
}

// record puts the volume's state on the gauges. A volume whose claim
// the driver has not found has no labels, so it reports nothing yet.
func (m *metrics) record(held *volume) {
	claim, armed, pending := held.reading()
	if m == nil || claim.name == "" {
		return
	}
	value := 0.0
	if armed {
		value = 1
	}
	m.armed.WithLabelValues(claim.namespace, claim.name).Set(value)
	m.pending.WithLabelValues(claim.namespace, claim.name).Set(float64(pending))
}

// forget takes a volume off the gauges, so a claim that is gone stops
// being reported.
func (m *metrics) forget(held *volume) {
	claim, _, _ := held.reading()
	if m == nil || claim.name == "" {
		return
	}
	m.armed.DeleteLabelValues(claim.namespace, claim.name)
	m.pending.DeleteLabelValues(claim.namespace, claim.name)
}

// listen opens the address --metrics names. An empty address serves no
// metrics, which is what a driver under test does.
func (m *metrics) listen(address string) (net.Listener, error) {
	if address == "" {
		return nil, nil
	}
	return net.Listen("tcp", address)
}

// handler serves the registry at /metrics and nothing else.
func (m *metrics) handler() http.Handler {
	served := http.NewServeMux()
	served.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return served
}

// serveMetrics answers on the listener until the run ends.
func serveMetrics(ctx context.Context, listener net.Listener, readings *metrics, logger *slog.Logger) {
	serving := &http.Server{
		Handler:           readings.handler(),
		ReadHeaderTimeout: metricsDeadline,
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.Serve(listener); err != nil && ctx.Err() == nil {
		logger.WarnContext(ctx, "the metrics listener stopped", "error", err)
	}
}
