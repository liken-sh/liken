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
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metricsDeadline bounds a request's headers and the listener's stop.
const metricsDeadline = 30 * time.Second

// metrics is the registry the listener serves and the gauges
// every volume reports itself on.
type metrics struct {
	registry *prometheus.Registry
	armed    *prometheus.GaugeVec
	pending  *prometheus.GaugeVec
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
	readings.registry.MustRegister(readings.armed, readings.pending,
		readings.unpushed, readings.lastPush, readings.pushFailures, readings.skipped,
		readings.diverged, readings.abnormal, readings.demandedPulls)
	return readings
}

// registerWebhook puts the listener's two counters on the registry. A
// node plugin answers no webhook, so its registry carries neither.
func (m *metrics) registerWebhook() {
	m.registry.MustRegister(m.webhookRequests, m.webhookMarks)
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
