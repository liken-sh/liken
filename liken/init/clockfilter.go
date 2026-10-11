package main

// The clock filter.
//
// One SNTP exchange measures the clock's offset plus half of the
// difference between the two directions of the trip. On a wired LAN
// the two directions take the same time, and the difference is
// microseconds. On wifi, or behind a busy uplink, one direction can
// wait in a queue that the other skips, and one exchange can be off by
// tens of milliseconds. A machine on wifi measured −30 ms, +37 ms, and
// −8 ms on three polls in a row from the same source, while its clock
// was fine.
//
// NTP's answer is the clock filter of RFC 5905, section 10, and this
// file keeps its core. The filter holds the last eight samples from
// one source and selects the one with the shortest round trip. A
// short trip leaves little room for its two directions to differ, so
// its offset is the closest to the truth. The slew, the bound in
// status, and the reading for Prometheus all take the selected sample.

import "time"

// filterSamples is how many polls the filter remembers, the same eight
// as NTP's.
const filterSamples = 8

// clockFilter holds the recent samples from the current source, oldest
// first, and the time of the last sample that the clock was slewed by.
type clockFilter struct {
	samples []timeSync
	slewed  time.Time
}

// observe adds one poll's sample and answers the selected sample, and
// whether it is fresh: whether the clock has not been slewed by it
// yet. A sample measured the clock before the corrections that came
// after it, so slewing by the same sample twice would correct the same
// error twice. RFC 5905 passes a sample to the clock discipline only
// when it is newer than the last one passed, for the same reason.
//
// The filter starts empty when the source changes, because two sources
// have different paths. It drops a sample older than eight polls, so a
// sample from before a long outage cannot win on its old round trip.
func (f *clockFilter) observe(s timeSync) (selected timeSync, fresh bool) {
	if len(f.samples) > 0 && f.samples[0].source != s.source {
		f.samples = nil
	}
	f.samples = append(f.samples, s)
	oldest := s.at.Add(-filterSamples * timePollInterval)
	for len(f.samples) > filterSamples || f.samples[0].at.Before(oldest) {
		f.samples = f.samples[1:]
	}
	selected = f.samples[0]
	for _, candidate := range f.samples[1:] {
		if candidate.delay < selected.delay {
			selected = candidate
		}
	}
	fresh = selected.at.After(f.slewed)
	if fresh {
		f.slewed = selected.at
	}
	return selected, fresh
}
