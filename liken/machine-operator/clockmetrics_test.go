package main

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/liken-sh/liken/liken/machine"
)

var scrapedAt = time.Date(2026, 10, 11, 14, 0, 0, 0, time.UTC)

// scrapeClock writes reading to a new directory, unless it is the zero
// reading, then scrapes a registry that has only the clock collector,
// with scrapedAt as the current time.
func scrapeClock(t *testing.T, reading machine.ClockReading) string {
	t.Helper()
	dir := t.TempDir()
	if !reading.At.IsZero() {
		if err := machine.WriteClockReading(dir, reading); err != nil {
			t.Fatalf("writing the clock reading: %v", err)
		}
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(clockCollector{dir: dir, now: func() time.Time { return scrapedAt }})
	return scrapeHandler(t, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
}

func TestTheClockMetricsReportTheLatestReading(t *testing.T) {
	body := scrapeClock(t, machine.ClockReading{
		Offset: -3210 * time.Microsecond, Delay: time.Millisecond,
		Source: "time.cloudflare.com", At: scrapedAt.Add(-30 * time.Second),
	})
	requireSeries(t, body, `liken_time_offset_seconds -0.00321`)
	requireSeries(t, body, `liken_time_last_sync_timestamp_seconds 1.79172717e+09`)
}

func TestAMachineWithNoReadingReportsNoClock(t *testing.T) {
	body := scrapeClock(t, machine.ClockReading{})
	requireNoSeries(t, body, "liken_time_offset_seconds")
	requireNoSeries(t, body, "liken_time_last_sync_timestamp_seconds")
}

func TestAStaleReadingDropsTheOffsetAndKeepsTheLastSync(t *testing.T) {
	body := scrapeClock(t, machine.ClockReading{
		Offset: time.Millisecond, Source: "10.0.0.210",
		At: scrapedAt.Add(-machine.ClockStaleAfter - time.Second),
	})
	requireNoSeries(t, body, "liken_time_offset_seconds")
	if len(series(body, "liken_time_last_sync_timestamp_seconds")) != 1 {
		t.Errorf("the scrape has no liken_time_last_sync_timestamp_seconds series, want one: %s", body)
	}
}
