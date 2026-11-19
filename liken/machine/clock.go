package machine

// init measures the clock every ClockPollInterval and writes two
// records. The sync state, source, stratum, and an OffsetBound go into
// the facts tree, and machine-operator copies them to status.time.
// The exact offset of each poll goes into a ClockReading file outside
// the facts tree, and machine-operator reads it only when Prometheus
// scrapes. machine-operator watches the whole facts tree with inotify,
// so a file in the tree that changed on every poll would start a
// reconcile pass and a status write on every poll.

import (
	"fmt"
	"path/filepath"
	"time"
)

// ClockPollInterval is how often init measures the clock. 64 seconds
// is NTP's default minimum poll interval (minpoll 6, or 2^6 seconds).
const ClockPollInterval = 64 * time.Second

// ClockStaleAfter is the age after which a measurement is out of date:
// one missed poll can be a busy source, but three mean the source is
// gone. Past this age, init reports the state Unsynchronized, and
// machine-operator stops publishing the offset of a reading that
// old.
const ClockStaleAfter = 3 * ClockPollInterval

// ClockDir is the directory of the ClockReading file, on the /run tmpfs
// beside the facts tree.
const ClockDir = "/run/liken/clock"

// An OffsetBound is the smallest limit in offsetBounds that the
// clock's offset does not exceed. status.time holds the bound and not
// the exact offset, so the status changes only when the clock gets
// better or worse, not on every poll.
type OffsetBound int

// offsetBounds are the limits. 1ms is a follower syncing from a leader
// on a quiet LAN. 5ms is a leader on a good internet upstream. 25ms is
// a noisy path, such as wifi. 128ms is ntpd's step threshold: ntpd
// slews a smaller offset and steps a larger one. 1s is a badly wrong
// clock that TLS and leases still tolerate. An offset above the last
// limit gets the bound len(offsetBounds), which prints as ">1s".
var offsetBounds = []time.Duration{
	time.Millisecond,
	5 * time.Millisecond,
	25 * time.Millisecond,
	128 * time.Millisecond,
	time.Second,
}

// stepThreshold is the bound of 128ms, ntpd's step threshold.
const stepThreshold OffsetBound = 3

// OffsetBoundOf returns the bound of an offset, positive or negative.
func OffsetBoundOf(offset time.Duration) OffsetBound {
	for i, limit := range offsetBounds {
		if offset.Abs() <= limit {
			return OffsetBound(i)
		}
	}
	return OffsetBound(len(offsetBounds))
}

// String formats the bound as status shows it, such as "±5ms", or
// ">1s" for a bound above the last limit.
func (b OffsetBound) String() string {
	if int(b) >= len(offsetBounds) {
		return ">" + offsetBounds[len(offsetBounds)-1].String()
	}
	return "±" + offsetBounds[b].String()
}

// ParseOffsetBound parses text that String wrote. It returns false for
// any other text, such as an exact offset.
func ParseOffsetBound(text string) (OffsetBound, bool) {
	for b := OffsetBound(0); int(b) <= len(offsetBounds); b++ {
		if b.String() == text {
			return b, true
		}
	}
	return 0, false
}

// OutrunsSlew reports whether the bound is above ntpd's step
// threshold of 128ms.
func (b OffsetBound) OutrunsSlew() bool {
	return b > stepThreshold
}

// A ClockReading is the result of one poll, after init's clock filter:
// the offset of the selected sample, its round-trip delay, the source
// that replied, and the time of the poll.
type ClockReading struct {
	Offset time.Duration
	Delay  time.Duration
	Source string
	At     time.Time
}

// WriteClockReading replaces the reading file under dir with a
// rename, so a scrape at the same time reads the old reading or the
// new one, never a mix. The file has the key=value format of the
// facts tree's record files.
func WriteClockReading(dir string, r ClockReading) error {
	return FactsTree{Dir: dir}.writeRecordFact("reading", [][2]string{
		{"offset", r.Offset.String()},
		{"delay", r.Delay.String()},
		{"source", r.Source},
		{"at", formatTime(&r.At)},
	})
}

// ReadClockReading reads the reading file under dir. When the file
// does not exist, because no poll has had a reply since boot, it
// returns the zero reading with no error.
func ReadClockReading(dir string) (ClockReading, error) {
	path := filepath.Join(dir, "reading")
	record, err := FactsTree{Dir: dir}.readRecordFact("reading")
	if err != nil || record == nil {
		return ClockReading{}, err
	}
	r := ClockReading{Source: record["source"]}
	if r.Offset, err = time.ParseDuration(record["offset"]); err != nil {
		return ClockReading{}, fmt.Errorf("parsing the offset in %s: %w", path, err)
	}
	if r.Delay, err = time.ParseDuration(record["delay"]); err != nil {
		return ClockReading{}, fmt.Errorf("parsing the delay in %s: %w", path, err)
	}
	if r.At, err = time.Parse(time.RFC3339Nano, record["at"]); err != nil {
		return ClockReading{}, fmt.Errorf("parsing the time in %s: %w", path, err)
	}
	return r, nil
}
