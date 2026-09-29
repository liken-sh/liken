package main

// The progress role's half of a mark. A person at the media browser marks
// a title watched or clears its progress, and the browser publishes the
// mark retained on its own topic. The mark must survive a progress role
// that is down when the person presses, and retention is how it survives:
// the broker delivers it when the role subscribes again.
//
// The role writes each mark as one whole row, the way it writes an outside
// play, and the row is the mark's own, named after it. A mark delivered
// again finds its row already recorded at its time and writes nothing, so
// a redelivery never changes a row that stands.
//
// Something must clear each retained mark, or the broker holds every mark
// ever pressed. The role clears a mark once it has recorded it and the
// retention has run from the press. The retention is longer than a
// restart of the jellyfin role, which reads the same message to send the
// mark to Jellyfin, so that role reads a mark it missed while it was down.

import (
	"context"
	"encoding/json"
	"time"
)

// How long the broker keeps a mark after the press. A variable, so a test
// runs a retention in milliseconds.
var markRetention = 24 * time.Hour

// recordMark writes one mark as one row and schedules its clear. An empty
// payload is the clear itself, which records nothing.
//
// A payload that reads as no mark is cleared at once, because nothing will
// ever record it and the broker would hold it for good. A mark the store
// refused stays on the broker, and the next subscription delivers it again.
func (p *progress) recordMark(ctx context.Context, name string, payload []byte) {
	if len(payload) == 0 {
		return
	}
	topic := playMarkTopic(p.topicBase, p.namespace, name)
	mark := titleMark{}
	if err := json.Unmarshal(payload, &mark); err != nil {
		p.logf("the mark %s reads as no mark, and the role cleared it: %v", name, err)
		p.bus.Publish(topic, nil, true)
		return
	}
	if mark.Mark != markWatched && mark.Mark != markCleared {
		p.logf("the mark %s names %q, which is neither %s nor %s, and the role cleared it",
			name, mark.Mark, markWatched, markCleared)
		p.bus.Publish(topic, nil, true)
		return
	}

	// A mark is a row with no Play behind it, so it is ended at the press.
	// The browser reads an ended row as a play that stopped, and never as
	// one in flight.
	err := p.store.recordOutside(ctx, name, outsidePlay{
		Player:   mark.Player,
		People:   mark.People,
		Aliases:  mark.Aliases,
		Season:   mark.Season,
		Episode:  mark.Episode,
		Position: mark.Position,
		Duration: mark.Duration,
		Ended:    true,
		At:       mark.At,
	})
	if err != nil {
		p.logf("could not record the mark %s: %v", name, err)
		return
	}
	p.logf("recorded the mark %s as %s at %d of %d for %d people, pressed at %s",
		name, mark.Mark, mark.Position, mark.Duration, len(mark.People),
		time.Unix(mark.At, 0).UTC().Format(time.RFC3339))

	wait := markClearIn(mark.At, time.Now())
	if wait == 0 {
		p.bus.Publish(topic, nil, true)
		return
	}
	// The timer is a clock: the retention of one mark. A role that restarts
	// before it fires reads the mark back on its next subscription and
	// schedules the clear again from the same press.
	time.AfterFunc(wait, func() { p.bus.Publish(topic, nil, true) })
}

// markClearIn is how long a mark pressed at this Unix time stays on the
// broker from now: the retention less the time since the press, and none
// once the retention has run.
func markClearIn(at int64, now time.Time) time.Duration {
	return max(time.Unix(at, 0).Add(markRetention).Sub(now), 0)
}
