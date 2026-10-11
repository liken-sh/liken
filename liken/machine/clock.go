package machine

// The two ways a machine's clock is observed.
//
// init measures the clock every ClockPollInterval and keeps two
// records of it. The standing, which is the state, the source, the
// stratum, and a bound on the offset, goes into the facts tree, and
// machine-operator copies it into status.time. It changes rarely, and
// each change is news, so each change is worth a status write. The
// reading, which is the exact offset of each poll, changes every poll
// and means nothing without its history. It goes to a file outside
// the facts tree, and machine-operator reads that file only when
// Prometheus scrapes it. machine-operator watches the whole facts tree
// with inotify, so a file written every poll inside the tree would
// wake a reconcile pass every poll, and each pass would cost the API
// server for a number that status does not hold.

import (
	"fmt"
	"path/filepath"
	"time"
)

// ClockPollInterval is how often init measures the clock. 64 seconds
// is NTP's classic starting interval (minpoll 6, or 2^6 seconds). It
// is frequent enough to catch drift measured in parts per million,
// and infrequent enough that it does not burden an upstream source.
const ClockPollInterval = 64 * time.Second

// ClockStaleAfter is how long a measurement stays current. A source
// that misses one poll is busy, and a source that misses three is
// gone. init reports Unsynchronized past this age, and
// machine-operator stops publishing the offset of a reading this old.
const ClockStaleAfter = 3 * ClockPollInterval

// ClockDir holds the reading. It is on /run, which is tmpfs, beside
// the facts tree and not inside it.
const ClockDir = "/run/liken/clock"

// An OffsetBound is the smallest of a fixed set of bounds that a
// clock's offset fits inside. Status publishes the bound instead of
// the exact offset, because the exact offset moves on every poll and
// each status write costs a raft round on every leader. A bound moves
// only when the clock's quality changes.
type OffsetBound int

// offsetBounds are the limits, each chosen for what it means. 1ms is
// a follower on a quiet LAN, syncing from a leader. 5ms is a leader on
// a good internet upstream. 25ms is a noisy path, such as wifi. 128ms
// is the line ntpd draws between slewing and stepping, so past it
// drift outruns the slew. 1s is a clock that is badly wrong, but still
// inside what TLS and leases tolerate. An offset past the last limit
// takes the bound one past the end of the table.
var offsetBounds = []time.Duration{
	time.Millisecond,
	5 * time.Millisecond,
	25 * time.Millisecond,
	128 * time.Millisecond,
	time.Second,
}

// slewLimit is the bound of the step threshold. A worse bound means
// the slew cannot keep up with the clock's error.
const slewLimit OffsetBound = 3

// OffsetBoundOf answers the smallest bound that offset fits inside, in
// either direction.
func OffsetBoundOf(offset time.Duration) OffsetBound {
	for i, limit := range offsetBounds {
		if offset.Abs() <= limit {
			return OffsetBound(i)
		}
	}
	return OffsetBound(len(offsetBounds))
}

// String answers the bound as status shows it: "±5ms", or ">1s" past
// the last limit.
func (b OffsetBound) String() string {
	if int(b) >= len(offsetBounds) {
		return ">" + offsetBounds[len(offsetBounds)-1].String()
	}
	return "±" + offsetBounds[b].String()
}

// ParseOffsetBound reads a bound that String wrote. It refuses any
// other text, such as the exact offset that an older release
// published, so a reader never takes a measurement for a bound.
func ParseOffsetBound(text string) (OffsetBound, bool) {
	for b := OffsetBound(0); int(b) <= len(offsetBounds); b++ {
		if b.String() == text {
			return b, true
		}
	}
	return 0, false
}

// OutrunsSlew reports whether the bound is past the step threshold,
// where the clock's error grows faster than a slew removes it.
func (b OffsetBound) OutrunsSlew() bool {
	return b > slewLimit
}

// A ClockReading is one poll's measurement, after init's clock filter:
// the offset of the selected sample, its round-trip delay, the source
// that answered, and the time of the poll.
type ClockReading struct {
	Offset time.Duration
	Delay  time.Duration
	Source string
	At     time.Time
}

// WriteClockReading replaces the reading under dir in one rename, so a
// scrape that reads it at the same moment sees the old reading or the
// new one, never half of each. The file is a record of key=value
// lines, in the grammar of the facts tree's record files.
func WriteClockReading(dir string, r ClockReading) error {
	return FactsTree{Dir: dir}.writeRecordFact("reading", [][2]string{
		{"offset", r.Offset.String()},
		{"delay", r.Delay.String()},
		{"source", r.Source},
		{"at", formatTime(&r.At)},
	})
}

// ReadClockReading reads the reading under dir. A missing file is the
// zero reading, whose zero At means that no poll has been answered
// this boot. A field that does not parse is an error that names the
// file.
func ReadClockReading(dir string) (ClockReading, error) {
	path := filepath.Join(dir, "reading")
	record, err := FactsTree{Dir: dir}.readRecordFact("reading")
	if err != nil || record == nil {
		return ClockReading{}, err
	}
	r := ClockReading{Source: record["source"]}
	if r.Offset, err = time.ParseDuration(record["offset"]); err != nil {
		return ClockReading{}, fmt.Errorf("%s: offset: %w", path, err)
	}
	if r.Delay, err = time.ParseDuration(record["delay"]); err != nil {
		return ClockReading{}, fmt.Errorf("%s: delay: %w", path, err)
	}
	if r.At, err = time.Parse(time.RFC3339Nano, record["at"]); err != nil {
		return ClockReading{}, fmt.Errorf("%s: at: %w", path, err)
	}
	return r, nil
}
