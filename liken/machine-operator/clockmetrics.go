package main

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/liken-sh/liken/liken/machine"
	"github.com/liken-sh/liken/liken/metrics"
)

var (
	clockOffsetDesc = prometheus.NewDesc(metrics.Prefix+"time_offset_seconds",
		"How far this machine's clock is from its time source, after the clock filter. Positive means the clock is behind. Absent before the first answered poll and once the last answer is three polls old.",
		nil, nil)
	clockLastSyncDesc = prometheus.NewDesc(metrics.Prefix+"time_last_sync_timestamp_seconds",
		"When a time source last answered this machine's poll, in seconds since the epoch.",
		nil, nil)
)

// clockCollector publishes the clock's offset on each scrape. The
// other metrics in metrics.go come from the status that a pass writes,
// but status has only a bound on the offset (machine/clock.go), and a
// status write after each poll would be a datastore write on every
// leader. So init writes each poll's reading to a file in dir, and
// Collect reads that file only during a scrape. now gives the current
// time, for the age of the reading.
type clockCollector struct {
	dir string
	now func() time.Time
}

func (c clockCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- clockOffsetDesc
	ch <- clockLastSyncDesc
}

// Collect publishes nothing when there is no reading or it cannot read
// one, because a zero offset would look like a perfect clock. When the
// reading is older than machine.ClockStaleAfter, the age at which init
// reports the clock Unsynchronized, Collect omits the offset but still
// publishes the last sync time, so a graph shows when the source
// stopped answering.
func (c clockCollector) Collect(ch chan<- prometheus.Metric) {
	reading, err := machine.ReadClockReading(c.dir)
	if err != nil || reading.At.IsZero() {
		return
	}
	ch <- prometheus.MustNewConstMetric(clockLastSyncDesc, prometheus.GaugeValue,
		float64(reading.At.UnixNano())/1e9)
	if c.now().Sub(reading.At) > machine.ClockStaleAfter {
		return
	}
	ch <- prometheus.MustNewConstMetric(clockOffsetDesc, prometheus.GaugeValue, reading.Offset.Seconds())
}
