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
// one source and prefers the one with the shortest round trip. A short
// trip leaves little room for its two directions to differ, so its
// offset is the closest to the truth.
//
// An old sample has a second kind of error: the clock has drifted
// since it was measured. RFC 5905 grows each sample's error bound at
// 15 ppm of its age, a rate that holds because ntpd also corrects the
// clock's frequency. liken slews the offset and leaves the frequency
// alone, so the clock drifts at whatever rate its crystal runs: about
// 50 ppm on the testbed's leader. So the filter selects the sample
// with the smallest error bound, half its round trip plus its age at
// driftTolerance. On a quiet wired path the newest sample nearly
// always wins, and on wifi an old sample wins only while the new ones
// are noisier than the drift since it. The slew, the bound in status,
// and the reading for Prometheus all take the selected sample.

import "time"

// filterSamples is how many polls the filter remembers, the same eight
// as NTP's.
const filterSamples = 8

// driftTolerance bounds how fast an undisciplined clock drifts: 100
// ppm, or 6.4 ms over one poll. It is twice the drift the testbed's
// leader showed, and inside the range of an ordinary crystal.
const driftTolerance = 100e-6

// errorBound is the most a sample's offset can be wrong by at now: half
// its round trip, for the asymmetry of the path, plus the drift since
// it was measured.
func errorBound(s filterSample, now time.Time) time.Duration {
	return s.delay/2 + time.Duration(float64(now.Sub(s.at))*driftTolerance)
}

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

// observe adds one poll's sample and answers the sample with the
// smallest error bound, with
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
		if errorBound(candidate, s.at) < errorBound(best, s.at) {
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
