package main

// metrics.go holds the registry, every gauge and counter the driver
// reports on it, and the listener that serves it. Layers 1 and 2 are
// liken's shared contract, milestone 65: the Go runtime's own numbers,
// the release the binary was built from, and the CSI operation the
// kubelet called, timed and counted the way an operator counts a
// reconcile. Layer 3 is what makes this driver its own: what is
// mounted, and the mounts that fail.

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

// metricsDeadline bounds a request's headers and the listener's stop.
const metricsDeadline = 30 * time.Second

// component is the name every process in the organization sets on
// liken_build_info, from the prefix table in milestone 65.
const component = "per-node-csi-driver"

// copyLabels is the one label on the copy gauge: the volume handle,
// which is the name a person looks the PersistentVolume up by.
var copyLabels = []string{"volume"}

// kindLabel is the one label layer 2 carries: the CSI operation a gRPC
// call named, or the resource kind a watch follows. liken's shared
// dashboard reads every repository's layer 2 under this same name.
var kindLabel = []string{"kind"}

// metrics is the registry the listener serves and every gauge and
// counter the driver reports on it.
type metrics struct {
	registry *prometheus.Registry

	// copyBytes is the bytes each copy holds, from the walk
	// NodeGetVolumeStats makes.
	copyBytes *prometheus.GaugeVec

	// reconcileDuration and reconcileErrors are layer 2. The kubelet's
	// call is this driver's reconcile: there is no other loop that
	// changes a volume's state, so every gRPC handler answers both.
	reconcileDuration *prometheus.HistogramVec
	reconcileErrors   *prometheus.CounterVec
	// watchRestarts counts a restart of the one watch the sweep holds,
	// on the PersistentVolumes of this driver.
	watchRestarts *prometheus.CounterVec

	// volumes is what is mounted on this node right now, and
	// mountFailures is the mounts that have failed since the driver
	// started.
	volumes       prometheus.Gauge
	mountFailures prometheus.Counter
}

func newMetrics() *metrics {
	readings := &metrics{
		registry: prometheus.NewRegistry(),
		copyBytes: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "per_node_copy_bytes",
				Help: "Bytes this node's copy of the volume holds.",
			}, copyLabels),
		reconcileDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "pernodecsi_reconcile_duration_seconds",
				Help: "How long a CSI operation took to answer.",
			}, kindLabel),
		reconcileErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pernodecsi_reconcile_errors_total",
				Help: "CSI operations that answered with an error.",
			}, kindLabel),
		watchRestarts: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "pernodecsi_watch_restarts_total",
				Help: "Times a watch dropped and opened again.",
			}, kindLabel),
		volumes: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name: "pernodecsi_volumes",
				Help: "Volumes this node holds a copy of for a pod that has published them.",
			}),
		mountFailures: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "pernodecsi_mount_failures_total",
				Help: "Mounts that failed.",
			}),
	}
	readings.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfo(),
		readings.copyBytes,
		readings.reconcileDuration,
		readings.reconcileErrors,
		readings.watchRestarts,
		readings.volumes,
		readings.mountFailures,
	)
	return readings
}

// buildInfo is the one gauge every process in the organization reports,
// so a single panel shows every release running in the cluster.
func buildInfo() prometheus.Collector {
	info := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "liken_build_info",
			Help: "The release the process was built from, value 1.",
		}, []string{"component", "version"})
	info.WithLabelValues(component, version).Set(1)
	return info
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

// recordCall puts a CSI operation's answer time on the histogram, and
// counts it as an error when the handler answered one. kind is the
// operation name, such as NodePublishVolume.
func (m *metrics) recordCall(kind string, duration time.Duration, err error) {
	m.reconcileDuration.WithLabelValues(kind).Observe(duration.Seconds())
	if err != nil {
		m.reconcileErrors.WithLabelValues(kind).Inc()
	}
}

// watchRestarted counts one restart of the watch on the resource kind.
func (m *metrics) watchRestarted(kind string) {
	m.watchRestarts.WithLabelValues(kind).Inc()
}

// setVolumes sets the volumes gauge to the number of handles this node
// holds right now. It is a Set and not an Add, because a hold that
// resume restores after a restart is not a new volume.
func (m *metrics) setVolumes(count int) {
	m.volumes.Set(float64(count))
}

// mountFailed counts one mount that failed.
func (m *metrics) mountFailed() {
	m.mountFailures.Inc()
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
