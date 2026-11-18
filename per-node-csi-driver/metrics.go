package main

// metrics.go defines the driver's Prometheus metrics and the HTTP
// listener that serves them. The Go and process metrics,
// liken_build_info, and the kind label have the same names in every
// liken component, so one dashboard query works for every component.

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsHeaderTimeout is how long a scrape request has to send its
// headers.
const metricsHeaderTimeout = 30 * time.Second

const component = "per-node-csi-driver"

// The volume label is the volume handle.
var copyLabels = []string{"volume"}

// The kind label is the CSI operation, or the resource kind that a
// watch lists.
var kindLabel = []string{"kind"}

type metrics struct {
	registry *prometheus.Registry

	// NodeGetVolumeStats sets copyBytes when it walks the copy.
	copyBytes *prometheus.GaugeVec

	// The driver has no reconcile loop. It changes a volume only when
	// the kubelet calls it, so the reconcile metrics, whose names every
	// liken component shares, time each CSI call.
	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	// watchRestarts counts each PersistentVolume watch that the sweep
	// opens after its first.
	watchRestarts *prometheus.CounterVec

	// volumes is the number of copies that at least one pod on this
	// node has mounted.
	volumes       prometheus.Gauge
	mountFailures prometheus.Counter
}

func newMetrics() *metrics {
	m := &metrics{
		registry: prometheus.NewRegistry(),
		copyBytes: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "per_node_csi_copy_bytes",
				Help: "Bytes in this node's copy of the volume.",
			}, copyLabels),
		reconcileDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "per_node_csi_reconcile_duration_seconds",
				Help: "Time until a CSI operation returns a response.",
			}, kindLabel),
		reconcileErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "per_node_csi_reconcile_errors_total",
				Help: "CSI operations that returned an error.",
			}, kindLabel),
		watchRestarts: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "per_node_csi_watch_restarts_total",
				Help: "Watches the driver opened again after one ended.",
			}, kindLabel),
		volumes: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "per_node_csi_volumes",
				Help: "Volumes that at least one pod on this node has mounted.",
			}),
		mountFailures: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "per_node_csi_mount_failures_total",
				Help: "Mounts that failed.",
			}),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo(),
		m.copyBytes,
		m.reconcileDuration,
		m.reconcileErrors,
		m.watchRestarts,
		m.volumes,
		m.mountFailures,
	)
	return m
}

func buildInfo() prometheus.Collector {
	info := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "liken_build_info",
			Help: "The release the process was built from, value 1.",
		}, []string{"component", "version"})
	info.WithLabelValues(component, version).Set(1)
	return info
}

func (m *metrics) record(handle string, bytes int64) {
	m.copyBytes.WithLabelValues(handle).Set(float64(bytes))
}

// forget deletes the handle's series, so a deleted copy is not
// reported with its last size.
func (m *metrics) forget(handle string) {
	m.copyBytes.DeleteLabelValues(handle)
}

// kind is the CSI operation name, such as NodePublishVolume.
func (m *metrics) recordCall(kind string, duration time.Duration, err error) {
	m.reconcileDuration.WithLabelValues(kind).Observe(duration.Seconds())
	if err != nil {
		m.reconcileErrors.WithLabelValues(kind).Inc()
	}
}

func (m *metrics) watchRestarted(kind string) {
	m.watchRestarts.WithLabelValues(kind).Inc()
}

// setVolumes sets the gauge instead of adding to it, because
// loadRecords rebuilds the mount records after a restart and the gauge
// must match the mounts that exist now.
func (m *metrics) setVolumes(count int) {
	m.volumes.Set(float64(count))
}

func (m *metrics) mountFailed() {
	m.mountFailures.Inc()
}

// listen returns a nil listener for an empty --metrics, so the driver
// serves no metrics.
func (m *metrics) listen(address string) (net.Listener, error) {
	if address == "" {
		return nil, nil
	}
	return net.Listen("tcp", address)
}

func (m *metrics) handler() http.Handler {
	served := http.NewServeMux()
	served.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))
	return served
}

func serveMetrics(ctx context.Context, listener net.Listener, m *metrics, logger *slog.Logger) {
	serving := &http.Server{
		Handler:           m.handler(),
		ReadHeaderTimeout: metricsHeaderTimeout,
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.Serve(listener); err != nil && ctx.Err() == nil {
		logger.WarnContext(ctx, "The metrics listener stopped", "error", err)
	}
}
