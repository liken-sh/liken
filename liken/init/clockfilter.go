package main

// The clock filter picks the best of the recent SNTP samples from one
// source. It follows the clock filter of RFC 5905, section 10.
//
// One SNTP exchange can be wrong by up to half its round trip, because
// the request and the response can take different times. On wifi or
// behind a busy uplink, one direction can wait in a queue that the
// other skips, and one exchange can be off by tens of milliseconds. A
// sample with a short round trip leaves little room for that error.
//
// An old sample also has error from the clock's drift since it was
// measured. RFC 5905 allows 15 ppm of drift, because ntpd also
// corrects the clock's frequency. liken slews the offset and does not
// change the frequency, so the clock drifts at its crystal's own rate,
// and errorBound allows driftTolerance.

import "time"

// filterSamples is how many samples the filter keeps, as in NTP.
const filterSamples = 8

// driftTolerance is the drift rate that errorBound assumes for a clock
// whose frequency is not corrected: 100 ppm, or 6.4 ms over one poll.
// That covers the drift of an ordinary crystal.
const driftTolerance = 100e-6

// errorBound is the largest error a sample's offset can have at now:
// half its round trip, for the asymmetry of the path, plus the drift
// since it was measured.
func errorBound(s filterSample, now time.Time) time.Duration {
	return s.delay/2 + time.Duration(float64(now.Sub(s.at))*driftTolerance)
}

// filterSample is a sample and the total correction that the kernel
// had applied to the clock when the sample was measured.
type filterSample struct {
	timeSync
	applied time.Duration
}

// clockFilter keeps the recent samples from the current source, oldest
// first, and the total correction that this boot has asked the kernel
// to apply.
type clockFilter struct {
	samples   []filterSample
	requested time.Duration
}

// observe adds one poll's sample and returns the sample with the
// smallest error bound, with its offset corrected to the clock's error
// now. applied is the total correction the kernel has applied so far:
// the total requested, less the part of the last slew still pending.
//
// The returned offset is the measured offset less the correction
// applied since the measurement. Without that, a sample with a short
// round trip would keep reporting an error that a slew had already
// removed, for as long as it stayed in the filter.
//
// The filter discards its samples when the source changes, because two
// sources have different paths. It drops a sample older than
// filterSamples polls, so a sample from before a long outage cannot
// win on its old round trip.
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

// slewed records a new slew by offset. The new slew replaces the part
// of the previous one that was still pending, so the total requested
// becomes the amount applied so far plus the new offset.
func (f *clockFilter) slewed(applied, offset time.Duration) {
	f.requested = applied + offset
}
