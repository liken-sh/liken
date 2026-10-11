package main

import (
	"testing"
	"time"
)

var filterStart = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

// sample builds the nth poll's measurement from one source, a poll
// interval apart.
func sample(n int, source string, offset, delay time.Duration) timeSync {
	return timeSync{
		source: source, stratum: 2, offset: offset, delay: delay,
		at: filterStart.Add(time.Duration(n) * timePollInterval),
	}
}

func observeAll(f *clockFilter, samples ...timeSync) (selected timeSync, fresh bool) {
	for _, s := range samples {
		selected, fresh = f.observe(s)
	}
	return selected, fresh
}

// Over wifi, the samples with a long round trip carry the asymmetric
// delay, and the one with the shortest round trip is closest to the
// truth.
func TestClockFilterSelectsTheShortestRoundTrip(t *testing.T) {
	f := &clockFilter{}
	selected, _ := observeAll(f,
		sample(0, "10.0.0.1", -30*time.Millisecond, 40*time.Millisecond),
		sample(1, "10.0.0.1", -2*time.Millisecond, 4*time.Millisecond),
		sample(2, "10.0.0.1", 37*time.Millisecond, 45*time.Millisecond),
	)
	if selected.offset != -2*time.Millisecond {
		t.Errorf("got %v", selected.offset)
	}
}

// A sample from more than eight polls ago measured a clock that the
// slews have corrected since, so it leaves the filter however short
// its round trip was.
func TestClockFilterDropsSamplesOlderThanEightPolls(t *testing.T) {
	f := &clockFilter{}
	selected, _ := observeAll(f,
		sample(0, "10.0.0.1", time.Millisecond, time.Millisecond),
		sample(9, "10.0.0.1", 4*time.Millisecond, 10*time.Millisecond),
	)
	if selected.offset != 4*time.Millisecond {
		t.Errorf("got %v", selected.offset)
	}
}

func TestClockFilterKeepsEightSamples(t *testing.T) {
	f := &clockFilter{}
	observeAll(f, sample(0, "10.0.0.1", time.Millisecond, time.Millisecond))
	for n := 1; n <= 8; n++ {
		observeAll(f, sample(n, "10.0.0.1", 4*time.Millisecond, 10*time.Millisecond))
	}
	if len(f.samples) != 8 {
		t.Errorf("the filter holds %d samples", len(f.samples))
	}
}

// Two sources have different paths, so one source's samples say
// nothing about the other's.
func TestClockFilterStartsEmptyWhenTheSourceChanges(t *testing.T) {
	f := &clockFilter{}
	selected, _ := observeAll(f,
		sample(0, "10.0.0.1", time.Millisecond, time.Millisecond),
		sample(1, "10.0.0.2", 4*time.Millisecond, 10*time.Millisecond),
	)
	if selected.source != "10.0.0.2" || selected.offset != 4*time.Millisecond {
		t.Errorf("got %+v", selected)
	}
}

// A selected sample is fresh once. Slewing by it a second time would
// correct the same error twice.
func TestClockFilterReportsEachSelectedSampleFreshOnce(t *testing.T) {
	f := &clockFilter{}
	_, first := f.observe(sample(0, "10.0.0.1", 3*time.Millisecond, time.Millisecond))
	_, second := f.observe(sample(1, "10.0.0.1", 9*time.Millisecond, 20*time.Millisecond))
	_, third := f.observe(sample(2, "10.0.0.1", 2*time.Millisecond, 500*time.Microsecond))
	if !first || second || !third {
		t.Errorf("fresh: %v %v %v", first, second, third)
	}
}
