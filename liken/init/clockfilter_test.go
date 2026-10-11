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

// observeAll feeds the filter samples measured while no correction was
// applied, and answers the last selection.
func observeAll(f *clockFilter, samples ...timeSync) (selected timeSync) {
	for _, s := range samples {
		selected = f.observe(s, 0)
	}
	return selected
}

// Over wifi, the samples with a long round trip carry the asymmetric
// delay, and the one with the shortest round trip is closest to the
// truth.
func TestClockFilterSelectsTheShortestRoundTrip(t *testing.T) {
	f := &clockFilter{}
	selected := observeAll(f,
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
	selected := observeAll(f,
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
	selected := observeAll(f,
		sample(0, "10.0.0.1", time.Millisecond, time.Millisecond),
		sample(1, "10.0.0.2", 4*time.Millisecond, 10*time.Millisecond),
	)
	if selected.source != "10.0.0.2" || selected.offset != 4*time.Millisecond {
		t.Errorf("got %+v", selected)
	}
}

// A sample measured the clock before the corrections applied after it.
// Once the clock has been slewed by a sample's whole offset, that
// sample says the clock is right, however short its round trip.
func TestClockFilterCorrectsEachSampleForTheSlewsAppliedSinceIt(t *testing.T) {
	f := &clockFilter{}
	f.observe(sample(0, "10.0.0.1", 37*time.Millisecond, time.Millisecond), 0)
	selected := f.observe(sample(1, "10.0.0.1", 9*time.Millisecond, 20*time.Millisecond), 37*time.Millisecond)
	if selected.delay != time.Millisecond || selected.offset != 0 {
		t.Errorf("got %+v", selected)
	}
}

// Part of a slew still pending is not applied yet, so a sample keeps
// that part of its offset.
func TestClockFilterKeepsTheOffsetASlewHasNotAppliedYet(t *testing.T) {
	f := &clockFilter{}
	f.observe(sample(0, "10.0.0.1", 100*time.Millisecond, time.Millisecond), 0)
	selected := f.observe(sample(1, "10.0.0.1", 70*time.Millisecond, 20*time.Millisecond), 30*time.Millisecond)
	if selected.offset != 70*time.Millisecond {
		t.Errorf("got %v", selected.offset)
	}
}

// An old sample's error bound grows by 6.4ms of drift for each poll of
// age, the same as half of a 12.8ms longer round trip. So a sample one
// poll newer wins while its round trip is less than 12.8ms longer.
func TestClockFilterWeighsAgeAgainstRoundTrip(t *testing.T) {
	cases := map[string]struct {
		newDelay time.Duration
		want     time.Duration
	}{
		"a newer sample 10ms slower wins":  {10 * time.Millisecond, 4 * time.Millisecond},
		"a newer sample 15ms slower loses": {15 * time.Millisecond, time.Millisecond},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			selected := observeAll(&clockFilter{},
				sample(0, "10.0.0.1", time.Millisecond, 0),
				sample(1, "10.0.0.1", 4*time.Millisecond, tc.newDelay))
			if selected.offset != tc.want {
				t.Errorf("got %v", selected.offset)
			}
		})
	}
}
