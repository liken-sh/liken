package main

// Metrics.go holds the gauges the node plugin exports and the
// listener that serves them. Every fact the driver reports reaches the
// Events, the driver's log, and these numbers.

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

// metricsComponent is this repository's name in liken's shared
// Prometheus contract, milestone 65 of the liken repository. Every
// process the driver runs reports the same name, so a panel that reads
// every component in the cluster finds this one under one label value.
const metricsComponent = "git-csi-driver"

// callBuckets bound the histograms the driver keeps of its own work: a
// CSI call the kubelet answers in a moment, and a fetch that can run to
// gitDeadline. One scale covers both, because a slow call and a slow
// fetch are the same question on the same graph.
var callBuckets = []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60}

// metrics is the registry the listener serves and the gauges
// every volume reports itself on.
type metrics struct {
	registry *prometheus.Registry

	// Layer 1 of liken's contract: the release this process runs, read
	// on every component's own graph with the same name.
	buildInfo *prometheus.GaugeVec
	// Layer 2: the CSI calls are this driver's reconcile loop, so kind
	// is the call's own name, the way the kubelet names it. A watch
	// the API closes and the node reopens is counted here too, though
	// only the node plugin ever watches anything.
	callDuration  *prometheus.HistogramVec
	callErrors    *prometheus.CounterVec
	watchRestarts *prometheus.CounterVec
	// Layer 3, plan 13: the forge behind a fetch, and the store's own
	// growth. Only the node plugin fetches or holds a store, so only
	// its registry ever carries these past their zero value.
	volumes       *volumesCollector
	fetchDuration *prometheus.HistogramVec
	fetchFailures *prometheus.CounterVec
	storeBytes    prometheus.Gauge

	armed   *prometheus.GaugeVec
	pending *prometheus.GaugeVec
	// What an armed volume adds: the commits the remote does not
	// hold, when a push last worked, how many failed, and how many files
	// the size guard left out.
	unpushed     *prometheus.GaugeVec
	lastPush     *prometheus.GaugeVec
	pushFailures *prometheus.CounterVec
	skipped      *prometheus.GaugeVec
	// What a diverged volume adds: the side branch it pushes to
	// instead of its ref.
	diverged *prometheus.GaugeVec
	// What every volume carries, read-only volumes included:
	// whether the volume's report says something is wrong with it.
	abnormal *prometheus.GaugeVec
	// The pulls a demand started, per volume.
	demandedPulls *prometheus.CounterVec
	// What the controller's webhook listener answered, and the
	// PersistentVolumes a verified push marked. The controller alone
	// registers these two.
	webhookRequests *prometheus.CounterVec
	webhookMarks    prometheus.Counter
}

// metricLabels name the claim a person would look up.
var metricLabels = []string{"namespace", "claim"}

// webhookLabels name what the listener answered: accepted,
// unauthenticated, malformed, or failed.
var webhookLabels = []string{"result"}

// healthLabels name the volume a person would look up: the
// namespace that holds it, and the CSI volume id, which is the one name
// a read-only volume has.
var healthLabels = []string{"namespace", "volume"}

// buildInfoLabels name the release, in the shape every component in
// the cluster uses.
var buildInfoLabels = []string{"component", "version"}

// callLabels name the CSI operation a call or a fetch failure belongs
// to. The watch restarts take the same label, holding the kind of
// resource the watch reads instead of a call's name.
var callLabels = []string{"kind"}

// repoLabels name the repository a fetch or a mount belongs to, by the
// short name the store already gives it. store.go says why that name
// carries no URL and no credential.
var repoLabels = []string{"repo"}

func newMetrics() *metrics {
	readings := &metrics{
		registry: prometheus.NewRegistry(),
		// Layer 1: one gauge, the same name every component in the
		// cluster reports, so a release shows on one graph everywhere.
		buildInfo: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "liken_build_info", Help: "The release this process runs. Always 1."}, buildInfoLabels),
		// Layer 2: the CSI calls are this driver's reconcile loop. A
		// call that changes nothing still counts as one duration
		// reading, the way milestone 65 counts a reconcile that changes
		// nothing as one run.
		callDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "gitcsi_reconcile_duration_seconds",
				Help: "How long a CSI call took to answer.", Buckets: callBuckets}, callLabels),
		callErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "gitcsi_reconcile_errors_total",
				Help: "CSI calls that answered an error."}, callLabels),
		// A watch the API closes and the node reopens is one restart.
		// Only the node plugin watches PersistentVolumes, so only its
		// registry moves this past zero.
		watchRestarts: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "gitcsi_watch_restarts_total",
				Help: "Watches the API closed that the driver reopened."}, callLabels),
		// Layer 3, plan 13: what the plan's table asks for. volumes is
		// wired to the node's own map of what is mounted in
		// registerNodeFacts, once a node exists to read.
		fetchDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: "gitcsi_fetch_duration_seconds",
				Help: "How long a fetch from the forge took.", Buckets: callBuckets}, repoLabels),
		fetchFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "gitcsi_fetch_failures_total",
				Help: "Fetches from the forge that failed."}, repoLabels),
		storeBytes: prometheus.NewGauge(
			prometheus.GaugeOpts{Name: "gitcsi_store_bytes",
				Help: "Bytes the store's bare repositories and work trees hold on this node."}),
		// One when a class of this driver arms the volume, zero when none
		// does.
		armed: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_armed", Help: "One when a class of the driver arms the volume, zero when none does."}, metricLabels),
		// How many paths the last scan found that the driver has not
		// committed.
		pending: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_pending_paths", Help: "Paths the last scan found that the driver has not committed."}, metricLabels),
		// Commits the work tree holds that the remote does not.
		unpushed: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_unpushed_commits", Help: "Commits the work tree holds that the remote does not."}, metricLabels),
		// When a push to the remote last worked.
		lastPush: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_last_push_timestamp_seconds", Help: "When a push to the remote last worked, in seconds since the epoch."}, metricLabels),
		// Pushes that failed since the driver started.
		pushFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "git_csi_push_failures_total", Help: "Pushes to the remote that failed."}, metricLabels),
		// Files the last commit left out, over the size guard.
		skipped: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_skipped_files", Help: "Files the last commit left out, over commit.maxFileSize."}, metricLabels),
		// One while the volume pushes to its side branch, zero
		// while it pushes to its ref.
		diverged: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_diverged", Help: "One while the volume pushes to its side branch, zero while it pushes to its ref."}, metricLabels),
		// One while the volume's report says something is wrong
		// with it, zero while it says nothing is.
		abnormal: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: "git_csi_volume_abnormal", Help: "One while the volume's report says something is wrong with it, zero while it says nothing is."}, healthLabels),
		// Pulls a demand on the volume's PersistentVolume started. A
		// counter, because it never goes down.
		demandedPulls: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "git_csi_demanded_pulls_total", Help: "Pulls a demand on the volume's PersistentVolume started."}, healthLabels),
		// The webhook requests the controller answered, by what it
		// answered.
		webhookRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: "git_csi_webhook_requests_total", Help: "Webhook requests the controller answered, by what it answered."}, webhookLabels),
		// The PersistentVolumes a verified push marked.
		webhookMarks: prometheus.NewCounter(
			prometheus.CounterOpts{Name: "git_csi_webhook_marked_total", Help: "PersistentVolumes a verified push marked."}),
	}
	readings.registry.MustRegister(
		// Layer 1 is the runtime's own numbers beside the build info.
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		// Layers 1 and 2 answer for every CSI call either plugin
		// serves, so both the controller and the node register them.
		readings.buildInfo, readings.callDuration, readings.callErrors,
		readings.armed, readings.pending,
		readings.unpushed, readings.lastPush, readings.pushFailures, readings.skipped,
		readings.diverged, readings.abnormal, readings.demandedPulls)
	return readings
}

// registerWebhook puts the listener's two counters on the registry. A
// node plugin answers no webhook, so its registry carries neither.
func (m *metrics) registerWebhook() {
	m.registry.MustRegister(m.webhookRequests, m.webhookMarks)
}

// registerNodeFacts puts plan 13's layer 3 on the registry, and the
// watch restart counter beside it: the facts only the node plugin has,
// because only it fetches, holds a store, or watches a
// PersistentVolume. byRepo answers what volumesByRepo reads live off
// the node, so gitcsi_volumes never drifts from its own accounting of
// what is mounted.
func (m *metrics) registerNodeFacts(byRepo func() map[string]float64) {
	m.volumes = newVolumesCollector(byRepo)
	m.registry.MustRegister(m.watchRestarts, m.fetchDuration, m.fetchFailures,
		m.storeBytes, m.volumes)
}

// reportBuildInfo sets liken_build_info to 1 under this process's
// version, the moment it is known: the Dockerfile's -ldflags, or dev
// for a build outside it.
func (m *metrics) reportBuildInfo(version string) {
	m.buildInfo.WithLabelValues(metricsComponent, version).Set(1)
}

// observeCall records one CSI call's duration under the operation's own
// name, and counts it again on gitcsi_reconcile_errors_total when it
// answered one. The CSI calls are this driver's reconcile loop, so a
// call that changes nothing still counts as one duration reading.
func (m *metrics) observeCall(kind string, duration time.Duration, failed bool) {
	m.callDuration.WithLabelValues(kind).Observe(duration.Seconds())
	if failed {
		m.callErrors.WithLabelValues(kind).Inc()
	}
}

// watchRestarted counts one watch the API closed that the driver
// reopened.
func (m *metrics) watchRestarted(kind string) {
	if m == nil {
		return
	}
	m.watchRestarts.WithLabelValues(kind).Inc()
}

// timeFetch runs one git fetch and records how long it took under the
// repository's short name. A fetch that answers an error also counts on
// gitcsi_fetch_failures_total, so a forge that is slow and a forge that
// is down read differently on the graph.
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

// setStoreBytes records the store's size, in the store's own bytes and
// not a share of the filesystem it lives on. sweep.go says why the
// measurement runs on the sweep's own timer instead of the scrape's.
func (m *metrics) setStoreBytes(bytes int64) {
	m.storeBytes.Set(float64(bytes))
}

// volumesCollector reports gitcsi_volumes{repo} from the node's own map
// of what is mounted, read fresh at every scrape rather than kept on a
// gauge of its own. A gauge a publish sets and an unpublish clears would
// drift the moment either one took a path the other did not expect; a
// map the node already keeps for NodeGetVolumeStats cannot.
type volumesCollector struct {
	desc *prometheus.Desc
	// byRepo counts the volumes the node holds, keyed by the
	// repository each one follows.
	byRepo func() map[string]float64
}

func newVolumesCollector(byRepo func() map[string]float64) *volumesCollector {
	return &volumesCollector{
		desc: prometheus.NewDesc("gitcsi_volumes",
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

// record puts the volume's state on the gauges. A volume whose claim
// the driver has not found has no labels, so it reports nothing yet.
func (m *metrics) record(held *volume) {
	claim, armed, pending := held.reading()
	if m == nil || claim.name == "" {
		return
	}
	m.armed.WithLabelValues(claim.namespace, claim.name).Set(gauge(armed))
	m.pending.WithLabelValues(claim.namespace, claim.name).Set(float64(pending))

	unpushed, lastPush, skipped := held.pushing()
	m.unpushed.WithLabelValues(claim.namespace, claim.name).Set(float64(unpushed))
	m.skipped.WithLabelValues(claim.namespace, claim.name).Set(float64(skipped))
	m.diverged.WithLabelValues(claim.namespace, claim.name).Set(gauge(held.divergedFrom() != ""))
	// A volume that has never pushed reports no time, because zero
	// would read as a push in 1970.
	if !lastPush.IsZero() {
		m.lastPush.WithLabelValues(claim.namespace, claim.name).
			Set(float64(lastPush.Unix()))
	}
}

// health puts the volume's report on the gauge. A volume whose
// namespace the driver has not found has no labels, so it reports
// nothing yet, the way the claim-labeled gauges do.
func (m *metrics) health(held *volume, abnormal bool) {
	namespace := held.namespace()
	if m == nil || namespace == "" {
		return
	}
	m.abnormal.WithLabelValues(namespace, held.id).Set(gauge(abnormal))
}

// noteHealth records the volume's health on the gauge and writes
// one line the moment it turns, at Warn when the report says something
// is wrong and at Info when it says nothing is. The Events carry the
// same facts, so nothing here posts one.
func (n *node) noteHealth(ctx context.Context, held *volume) {
	abnormal, message, moved := held.takeHealth()
	n.readings.health(held, abnormal)
	if !moved {
		return
	}
	if abnormal {
		n.logger.WarnContext(ctx, "the volume is abnormal",
			"volume", held.id, "report", message)
		return
	}
	n.logger.InfoContext(ctx, "the volume is normal",
		"volume", held.id, "report", message)
}

// demanded counts one pull a demand started, under the labels
// git_csi_volume_abnormal takes.
func (m *metrics) demanded(held *volume) {
	namespace := held.namespace()
	if m == nil || namespace == "" {
		return
	}
	m.demandedPulls.WithLabelValues(namespace, held.id).Inc()
}

// webhookAnswered counts one request by what the listener answered.
func (m *metrics) webhookAnswered(result string) {
	m.webhookRequests.WithLabelValues(result).Inc()
}

// webhookMarked counts the PersistentVolumes one verified push marked,
// which is zero when the push matched nothing.
func (m *metrics) webhookMarked(count int) {
	m.webhookMarks.Add(float64(count))
}

// pushFailed counts one failure, which is the only reading a
// volume reports that never goes down.
func (m *metrics) pushFailed(held *volume) {
	claim, _, _ := held.reading()
	if m == nil || claim.name == "" {
		return
	}
	m.pushFailures.WithLabelValues(claim.namespace, claim.name).Inc()
}

// forget takes a volume off the gauges, so a claim that is gone stops
// being reported.
func (m *metrics) forget(held *volume) {
	if m == nil {
		return
	}
	// The health gauge is labeled by the volume, so it goes
	// whether or not the driver ever found a claim for it.
	if namespace := held.namespace(); namespace != "" {
		m.abnormal.DeleteLabelValues(namespace, held.id)
		m.demandedPulls.DeleteLabelValues(namespace, held.id)
	}
	claim, _, _ := held.reading()
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

// gauge carries a state as one or zero.
func gauge(state bool) float64 {
	if state {
		return 1
	}
	return 0
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
