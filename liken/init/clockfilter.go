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

// filterSample is one sample and the total correction the kernel had
// applied to the clock when the sample was measured.
type filterSample struct {
	timeSync
	applied time.Duration
}

// clockFilter holds the recent samples from the current source, oldest
// first, and the total correction this boot has asked the kernel for.
type clockFilter struct {
	samples   []filterSample
	requested time.Duration
}

// observe adds one poll's sample and answers the selected sample, with
// its offset corrected to the clock's error now. applied is the total
// correction the kernel has applied to the clock so far, which is the
// total requested less the part of the last slew still pending.
//
// A sample measured the clock as it was then. Each slew applied since
// then has moved the clock by part of that error, so the sample's
// offset now is its measured offset less the correction applied since
// it was measured. Without that, a sample with a short round trip
// would keep reporting an error that the slew had already removed, for
// as long as it stayed in the filter. chrony corrects its stored
// samples for each clock adjustment in the same way.
//
// The filter starts empty when the source changes, because two sources
// have different paths. It drops a sample older than eight polls, so a
// sample from before a long outage cannot win on its old round trip.
func (f *clockFilter) observe(s timeSync, applied time.Duration) timeSync {
	if len(f.samples) > 0 && f.samples[0].source != s.source {
		f.samples = nil
	}
	f.samples = append(f.samples, filterSample{timeSync: s, applied: applied})
	oldest := s.at.Add(-filterSamples * timePollInterval)
	for len(f.samples) > filterSamples || f.samples[0].at.Before(oldest) {
		f.samples = f.samples[1:]
	}
	best := f.samples[0]
	for _, candidate := range f.samples[1:] {
		if candidate.delay < best.delay {
			best = candidate
		}
	}
	selected := best.timeSync
	selected.offset -= applied - best.applied
	return selected
}

// slewed records a slew by offset that replaced whatever part of the
// last slew was still pending. The kernel discards that pending part,
// so the total requested becomes what was applied plus the new slew.
func (f *clockFilter) slewed(applied, offset time.Duration) {
	f.requested = applied + offset
}
