package main

// metrics.go holds the one gauge the node plugin exports and the
// listener that serves it.

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

// metrics is the registry the listener serves and the gauge every
// published copy on this node reports itself on.
type metrics struct {
	registry *prometheus.Registry
	// copyBytes is the bytes each copy holds, from the walk
	// NodeGetVolumeStats makes.
	copyBytes *prometheus.GaugeVec
}

// copyLabels is the one label on the gauge: the volume handle, which is
// the name a person looks the PersistentVolume up by.
var copyLabels = []string{"volume"}

func newMetrics() *metrics {
	readings := &metrics{
		registry: prometheus.NewRegistry(),
		copyBytes: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "per_node_copy_bytes",
				Help: "Bytes this node's copy of the volume holds.",
			}, copyLabels),
	}
	readings.registry.MustRegister(readings.copyBytes)
	return readings
}

// record puts the copy's bytes on the gauge, from the same walk the
// stats answer reports.
func (m *metrics) record(handle string, bytes int64) {
	m.copyBytes.WithLabelValues(handle).Set(float64(bytes))
}

// forget takes the copy off the gauge, so a volume this node has
// unpublished or swept is not reported.
func (m *metrics) forget(handle string) {
	m.copyBytes.DeleteLabelValues(handle)
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
