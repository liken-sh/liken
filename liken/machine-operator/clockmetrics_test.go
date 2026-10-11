package main

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/liken-sh/liken/liken/machine"
)

var scrapedAt = time.Date(2026, 10, 11, 14, 0, 0, 0, time.UTC)

// scrapeClock writes reading under a fresh directory, unless it is the
// zero reading, and scrapes a registry that holds only the clock's
// collector, at scrapedAt.
func scrapeClock(t *testing.T, reading machine.ClockReading) string {
	t.Helper()
	dir := t.TempDir()
	if !reading.At.IsZero() {
		if err := machine.WriteClockReading(dir, reading); err != nil {
			t.Fatal(err)
		}
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(clockCollector{dir: dir, now: func() time.Time { return scrapedAt }})
	return scrapeHandler(t, promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
}

func TestTheClockMetricsCarryTheLatestReading(t *testing.T) {
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

// A reading older than three polls is no longer the clock's offset, so
// it leaves the scrape, and the last-sync time stays to show when the
// source went quiet.
func TestAStaleReadingDropsTheOffsetAndKeepsTheLastSync(t *testing.T) {
	body := scrapeClock(t, machine.ClockReading{
		Offset: time.Millisecond, Source: "10.0.0.210",
		At: scrapedAt.Add(-machine.ClockStaleAfter - time.Second),
	})
	requireNoSeries(t, body, "liken_time_offset_seconds")
	if len(series(body, "liken_time_last_sync_timestamp_seconds")) != 1 {
		t.Errorf("the last sync left the scrape: %s", body)
	}
}
