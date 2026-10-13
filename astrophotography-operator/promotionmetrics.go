package main

// The series of promotion that plan 08 names: the frames archived, and
// the bytes and the failures of promotion. Each counts from the reports
// that the loop applies, once for each report, because a report is
// applied once (promotionstatus.go).

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/liken-sh/liken/astrophotography-operator/promote"
)

type promotionMetrics struct {
	framesArchived prometheus.Counter
	bytes          prometheus.Counter
	failures       *prometheus.CounterVec
}

// promotionFailureReasons are the reasons a frame fails, which label
// the failures from the first scrape.
var promotionFailureReasons = []string{promote.ReasonCopyFailed, promote.ReasonVerifyFailed, promote.ReasonNameConflict}

func newPromotionMetrics(m *metrics) *promotionMetrics {
	p := &promotionMetrics{
		framesArchived: prometheus.NewCounter(prometheus.CounterOpts{
			Name: metricsPrefix + "frames_archived_total",
			Help: "Frames that promotion Jobs moved from an outbox to an archive.",
		}),
		bytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: metricsPrefix + "promotion_bytes_total",
			Help: "Bytes that promotion Jobs copied from an outbox to an archive.",
		}),
		failures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: metricsPrefix + "promotion_failures_total",
			Help: "Frames that a promotion Job failed to move, by reason.",
		}, []string{"reason"}),
	}
	m.registry.MustRegister(p.framesArchived, p.bytes, p.failures)
	for _, reason := range promotionFailureReasons {
		p.failures.WithLabelValues(reason)
	}
	return p
}

// observe counts one report.
func (p *promotionMetrics) observe(r promote.Report) {
	p.framesArchived.Add(float64(r.Promoted))
	p.bytes.Add(float64(r.Bytes))
	for _, failure := range r.Failures {
		p.failures.WithLabelValues(failure.Reason).Inc()
	}
}
