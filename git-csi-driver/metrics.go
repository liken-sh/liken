package main

// metrics.go defines the Prometheus metrics of the controller and node
// plugins, and the HTTP listener that serves them.

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

// metricsDeadline is how long a client may take to send its request
// headers to the metrics listener.
const metricsDeadline = 30 * time.Second

// metricsComponent is the value of the component label on
// liken_build_info. Every liken component reports that metric, so a
// dashboard can list the releases of all of them with one query.
const metricsComponent = "git-csi-driver"

// callBuckets are the histogram buckets, in seconds, for CSI calls and
// for fetches, so both can share a graph. They run from a fast CSI call
// up to gitDeadline, the longest a fetch can take.
var callBuckets = []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}

// metrics is the registry that the listener serves, and the metrics that
// the driver updates as it works. Most are per volume.
type metrics struct {
	registry *prometheus.Registry

	buildInfo *prometheus.GaugeVec
	// A CSI call does the work that a reconcile does in the other liken
	// components, so it is reported under the same metric names, with
	// the call's name, such as NodePublishVolume, as kind.
	callDuration *prometheus.HistogramVec
	callErrors   *prometheus.CounterVec
	// Only the node plugin registers these. See registerNodeFacts.
	watchRestarts *prometheus.CounterVec
	volumes       *volumesCollector
	fetchDuration *prometheus.HistogramVec
	fetchFailures *prometheus.CounterVec
	storeBytes    prometheus.Gauge

	armed   *prometheus.GaugeVec
	pending *prometheus.GaugeVec
	// Push state of an armed volume: commits the remote does not have,
	// the time of the last successful push, failed pushes, and files
	// left out of the last commit because of the size limit.
	unpushed     *prometheus.GaugeVec
	lastPush     *prometheus.GaugeVec
	pushFailures *prometheus.CounterVec
	skipped      *prometheus.GaugeVec
	// Whether the volume pushes to a side branch instead of its ref.
	diverged *prometheus.GaugeVec
	// Whether the health report of the volume shows a problem. Every
	// volume has one, including read-only volumes.
	abnormal *prometheus.GaugeVec
	// Pulls that a pull-on-demand request started, per volume.
	demandedPulls *prometheus.CounterVec
	// Only the controller registers these. See registerWebhook.
	webhookRequests *prometheus.CounterVec
	webhookMarks    prometheus.Counter
}

// metricLabels identify the claim of a volume.
var metricLabels = []string{"namespace", "claim"}

// webhookLabels label a webhook request by its result: accepted,
// unauthenticated, malformed, or failed.
var webhookLabels = []string{"result"}

// healthLabels identify a volume by its namespace and its CSI volume id.
// A read-only volume has no claim of its own, so the volume id is the
// only name it has.
var healthLabels = []string{"namespace", "volume"}

var buildInfoLabels = []string{"component", "version"}

// callLabels name the CSI call that a measurement belongs to. For
// watchRestarts, kind holds the resource kind that the watch reads.
var callLabels = []string{"kind"}

// repoLabels label a fetch or a mount with the short name that the
// store gives its repository. store.go says why that name has no URL and
// no credential.
var repoLabels = []string{"repo"}

func newMetrics() *metrics {
	registry := &metrics{
		registry: prometheus.NewRegistry(),
		buildInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "liken_build_info", Help: "The release this process runs. Always 1."}, buildInfoLabels),
		callDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "git_csi_reconcile_duration_seconds",
				Help: "Time until a CSI call returns a response.", Buckets: callBuckets}, callLabels),
		callErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "git_csi_reconcile_errors_total",
				Help: "CSI calls that returned an error."}, callLabels),
		// client-go's reflector asks the API server to close each
		// watch after 5 to 10 minutes, so a healthy watch restarts 6
		// to 12 times an hour. A higher rate means that watches fail.
		watchRestarts: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "git_csi_watch_restarts_total",
				Help: "Watches the driver opened again after one ended."}, callLabels),
		fetchDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "git_csi_fetch_duration_seconds",
				Help: "How long a fetch from the forge took.", Buckets: callBuckets}, repoLabels),
		fetchFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "git_csi_fetch_failures_total",
				Help: "Fetches from the forge that failed."}, repoLabels),
		storeBytes: prometheus.NewGauge(
			prometheus.GaugeOpts{Name: "git_csi_store_bytes",
				Help: "Bytes that the bare repositories and work trees of the store use on this node."}),
		armed: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_armed", Help: "One when a class of the driver arms the volume, zero when none does."}, metricLabels),
		pending: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_pending_paths", Help: "Paths the last scan found that the driver has not committed."}, metricLabels),
		unpushed: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_unpushed_commits", Help: "Commits in the work tree that the remote does not have."}, metricLabels),
		lastPush: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_last_push_timestamp_seconds", Help: "When a push to the remote last worked, in seconds since the epoch."}, metricLabels),
		pushFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "git_csi_push_failures_total", Help: "Pushes to the remote that failed."}, metricLabels),
		skipped: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_skipped_files", Help: "Files the last commit left out, over commit.maxFileSize."}, metricLabels),
		diverged: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_diverged", Help: "One while the volume pushes to its side branch, zero while it pushes to its ref."}, metricLabels),
		abnormal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_volume_abnormal", Help: "One while the health report of the volume shows a problem, zero while it shows none."}, healthLabels),
		demandedPulls: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "git_csi_demanded_pulls_total", Help: "Pulls that the pull annotation on the volume's PersistentVolume started."}, healthLabels),
		webhookRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "git_csi_webhook_requests_total", Help: "Webhook requests that the controller received, by result."}, webhookLabels),
		webhookMarks: prometheus.NewCounter(
			prometheus.CounterOpts{Name: "git_csi_webhook_marked_total", Help: "PersistentVolumes that a verified push annotated for a pull."}),
	}
	registry.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		// The controller registers the per-volume gauges too, but
		// only the node plugin sets them, because the controller
		// mounts no volume.
		registry.buildInfo, registry.callDuration, registry.callErrors,
		registry.armed, registry.pending,
		registry.unpushed, registry.lastPush, registry.pushFailures, registry.skipped,
		registry.diverged, registry.abnormal, registry.demandedPulls)
	return registry
}

// registerWebhook registers the webhook counters. Only the controller
// serves the webhook, so only the controller calls it.
func (m *metrics) registerWebhook() {
	m.registry.MustRegister(m.webhookRequests, m.webhookMarks)
}

// registerNodeFacts registers the metrics of the node plugin, which is
// the only plugin that fetches, keeps a store, and watches
// PersistentVolumes and claims. byRepo returns the number of mounted
// volumes for each repository.
func (m *metrics) registerNodeFacts(byRepo func() map[string]float64) {
	m.volumes = newVolumesCollector(byRepo)
	m.registry.MustRegister(m.watchRestarts, m.fetchDuration, m.fetchFailures,
		m.storeBytes, m.volumes)
}

// reportBuildInfo sets liken_build_info to 1 for this process's version.
// The Dockerfile sets the version with -ldflags. A build without it
// reports dev.
func (m *metrics) reportBuildInfo(version string) {
	m.buildInfo.WithLabelValues(metricsComponent, version).Set(1)
}

// observeCall records the duration of one CSI call, and counts the call
// in git_csi_reconcile_errors_total when it returned an error.
func (m *metrics) observeCall(kind string, duration time.Duration, failed bool) {
	m.callDuration.WithLabelValues(kind).Observe(duration.Seconds())
	if failed {
		m.callErrors.WithLabelValues(kind).Inc()
	}
}

func (m *metrics) watchRestarted(kind string) {
	if m == nil {
		return
	}
	m.watchRestarts.WithLabelValues(kind).Inc()
}

// timeFetch runs one git fetch and records its duration. A failed fetch
// is also counted in git_csi_fetch_failures_total, so a graph can tell a
// slow forge from a forge that is down.
func (m *metrics) timeFetch(repo string, run func() error) error {
	start := time.Now()
	err := run()
	if m == nil {
		return err
	}
	m.fetchDuration.WithLabelValues(repo).Observe(time.Since(start).Seconds())
	if err != nil {
		m.fetchFailures.WithLabelValues(repo).Inc()
	}
	return err
}

// setStoreBytes records the bytes that the store itself uses, not the
// size of the filesystem that holds it. sweep.go says why the sweep
// measures it and not each scrape.
func (m *metrics) setStoreBytes(bytes int64) {
	m.storeBytes.Set(float64(bytes))
}

// volumesCollector reports git_csi_volumes by counting the node's map of
// mounted volumes on each scrape. A gauge that publish incremented and
// unpublish decremented would drift whenever either returned early on an
// error.
type volumesCollector struct {
	desc   *prometheus.Desc
	byRepo func() map[string]float64
}

func newVolumesCollector(byRepo func() map[string]float64) *volumesCollector {
	return &volumesCollector{
		desc: prometheus.NewDesc("git_csi_volumes",
			"Volumes this node has mounted, by the repository each one follows.",
			repoLabels, nil),
		byRepo: byRepo,
	}
}

func (c *volumesCollector) Describe(descriptions chan<- *prometheus.Desc) {
	descriptions <- c.desc
}

func (c *volumesCollector) Collect(collected chan<- prometheus.Metric) {
	for repo, count := range c.byRepo() {
		collected <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, count, repo)
	}
}

// record sets the gauges of a volume. The gauges are labeled by claim,
// so record does nothing until the driver has found the volume's claim.
func (m *metrics) record(vol *volume) {
	claim, armed, pending := vol.gaugeValues()
	if m == nil || claim.name == "" {
		return
	}
	m.armed.WithLabelValues(claim.namespace, claim.name).Set(gauge(armed))
	m.pending.WithLabelValues(claim.namespace, claim.name).Set(float64(pending))

	unpushed, lastPush, skipped := vol.pushing()
	m.unpushed.WithLabelValues(claim.namespace, claim.name).Set(float64(unpushed))
	m.skipped.WithLabelValues(claim.namespace, claim.name).Set(float64(skipped))
	m.diverged.WithLabelValues(claim.namespace, claim.name).Set(gauge(vol.sideBranch() != ""))
	// A volume that has never pushed gets no series, because a value of
	// zero would read as a push in 1970.
	if !lastPush.IsZero() {
		m.lastPush.WithLabelValues(claim.namespace, claim.name).
			Set(float64(lastPush.Unix()))
	}
}

// health sets git_csi_volume_abnormal for a volume. It does nothing
// until the driver has found the volume's namespace.
func (m *metrics) health(vol *volume, abnormal bool) {
	namespace := vol.namespace()
	if m == nil || namespace == "" {
		return
	}
	m.abnormal.WithLabelValues(namespace, vol.id).Set(gauge(abnormal))
}

// recordHealth sets the health gauge of the volume. When the health has
// changed, it logs once, at Warn when the volume became abnormal and at
// Info when it became normal. It posts no Event.
func (n *node) recordHealth(ctx context.Context, vol *volume) {
	abnormal, message, moved := vol.healthChange()
	n.metrics.health(vol, abnormal)
	if !moved {
		return
	}
	if abnormal {
		n.logger.WarnContext(ctx, "The volume became abnormal",
			"volume", vol.id, "report", message)
		return
	}
	n.logger.InfoContext(ctx, "The volume became normal",
		"volume", vol.id, "report", message)
}

// demanded counts one pull that a pull-on-demand request started. It
// uses the labels of git_csi_volume_abnormal.
func (m *metrics) demanded(vol *volume) {
	namespace := vol.namespace()
	if m == nil || namespace == "" {
		return
	}
	m.demandedPulls.WithLabelValues(namespace, vol.id).Inc()
}

func (m *metrics) countWebhookRequest(result string) {
	m.webhookRequests.WithLabelValues(result).Inc()
}

// webhookMarked adds the number of PersistentVolumes that one verified
// push marked, which is zero when the push matched no volume.
func (m *metrics) webhookMarked(count int) {
	m.webhookMarks.Add(float64(count))
}

func (m *metrics) pushFailed(vol *volume) {
	claim, _, _ := vol.gaugeValues()
	if m == nil || claim.name == "" {
		return
	}
	m.pushFailures.WithLabelValues(claim.namespace, claim.name).Inc()
}

// forget deletes the series of a volume, so a volume that is gone stops
// being reported.
func (m *metrics) forget(vol *volume) {
	if m == nil {
		return
	}
	// These series are labeled by volume id, so they can exist for a
	// volume that has no claim.
	if namespace := vol.namespace(); namespace != "" {
		m.abnormal.DeleteLabelValues(namespace, vol.id)
		m.demandedPulls.DeleteLabelValues(namespace, vol.id)
	}
	claim, _, _ := vol.gaugeValues()
	if claim.name == "" {
		return
	}
	m.armed.DeleteLabelValues(claim.namespace, claim.name)
	m.pending.DeleteLabelValues(claim.namespace, claim.name)
	m.unpushed.DeleteLabelValues(claim.namespace, claim.name)
	m.lastPush.DeleteLabelValues(claim.namespace, claim.name)
	m.pushFailures.DeleteLabelValues(claim.namespace, claim.name)
	m.skipped.DeleteLabelValues(claim.namespace, claim.name)
	m.diverged.DeleteLabelValues(claim.namespace, claim.name)
}

func gauge(state bool) float64 {
	if state {
		return 1
	}
	return 0
}

// listen opens the TCP listener for the address in --metrics. For an
// empty address it returns no listener, which tests use to serve no
// metrics.
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

func serveMetrics(ctx context.Context, listener net.Listener, registry *metrics, logger *slog.Logger) {
	serving := &http.Server{
		Handler:           registry.handler(),
		ReadHeaderTimeout: metricsDeadline,
	}
	go func() {
		<-ctx.Done()
		_ = serving.Close()
	}()
	if err := serving.Serve(listener); err != nil && ctx.Err() == nil {
		logger.WarnContext(ctx, "The metrics listener stopped", "error", err)
	}
}
