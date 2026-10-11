package main

// The clock's metrics, read at scrape time.
//
// Every other metric in metrics.go reads the status that a pass is
// about to write. The clock's offset cannot, because status holds only
// a bound on it (machine/clock.go): the exact offset moves on every
// poll, and a status write for each poll would cost a raft round on
// every leader. So init writes each poll's reading to a file outside
// the facts tree, and this collector reads that file only when
// Prometheus scrapes. A machine that nobody scrapes never reads it, and
// no pass ever wakes for it.

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

// clockCollector reads the reading under dir at each scrape. now is
// the clock it judges the reading's age by.
type clockCollector struct {
	dir string
	now func() time.Time
}

func (c clockCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- clockOffsetDesc
	ch <- clockLastSyncDesc
}

// Collect publishes nothing before the first answered poll, and
// nothing for a reading it cannot parse, because a zero would read as
// a perfect clock. It drops the offset of a reading older than
// machine.ClockStaleAfter, the age at which init reports the clock
// Unsynchronized, so a graph never shows an old offset as current.
// The last-sync time stays, so the graph shows when the source went
// quiet.
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
