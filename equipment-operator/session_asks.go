package main

// The asks in status.session: a volume, a power action, and an input
// action that the media operator writes for a person's press. Each ask
// carries the moment it was made, and a new moment is a new ask. The
// operator applies each ask once and never holds the receiver to it,
// so a person with the receiver's own remote can still change the
// room, the same contract the session's active and awake flags follow.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// seenAsks holds the at of the last ask of each kind the unit has
// read. The unit keeps it, not the session, so a session that a new
// input replaces does not send the asks it carries over again. Only the
// pass goroutine reads and writes it.
type seenAsks struct {
	volume, power, input string
}

// takeAsks applies each ask whose at differs from the last one the unit
// read. An ask the operator finds in its first pass after a start is
// recorded and sends nothing: a person can have turned the knob or
// changed the input since the media operator made it, and the last
// operator may have applied it already.
func (u *receiverUnit) takeAsks(spec *ReceiverSession, adopting bool) {
	if spec == nil {
		return
	}
	u.mutex.Lock()
	held := u.session
	u.mutex.Unlock()
	if ask := spec.VolumeAsk; ask != nil && ask.At != u.seen.volume {
		u.seen.volume = ask.At
		if !adopting {
			u.volumeAsks.put(*ask)
		}
	}
	if ask := spec.PowerAsk; ask != nil && ask.At != u.seen.power {
		u.seen.power = ask.At
		if !adopting && held != nil {
			source := "status.session.powerAsk at " + ask.At
			goWork(held.ctx, func() { held.power(ask.Action, source) })
		}
	}
	if ask := spec.InputAsk; ask != nil && ask.At != u.seen.input {
		u.seen.input = ask.At
		if !adopting && held != nil {
			held.inputAsk(*ask)
		}
	}
}

// carryAsks takes the asks the unit that a new wiring replaced had
// read, so the new unit does not send them again.
func (u *receiverUnit) carryAsks(replaced *receiverUnit) {
	u.seen = replaced.seen
}

// inputAsk runs an ensure or a show (session_ensure.go). An action the
// session does not know does nothing.
func (s *session) inputAsk(ask ReceiverInputAsk) {
	trigger := fmt.Sprintf("status.session.inputAsk at %s asks %s", ask.At, ask.Action)
	switch ask.Action {
	case "ensure":
		s.ensure(trigger)
	case "show":
		s.show(trigger)
	}
}

// pendingVolumeWait is how long a volume ask waits for the receiver to
// report the volume it was sent before the next ask goes out. A Denon
// reports within a fraction of a second. A receiver that never reports
// the volume, such as one whose own limit is below it, releases the
// next ask after this wait.
const pendingVolumeWait = time.Second

// volumeAsker holds the newest volume ask the receiver has not been
// sent. A held key writes an ask for each repeat, and a receiver
// reports each volume tens of milliseconds after it takes it. A queue
// would send every step the key passed through, and the receiver would
// trail the key. So an ask that arrives while another is unsent
// replaces it, and the receiver goes straight to the newest level.
type volumeAsker struct {
	mutex sync.Mutex
	next  *ReceiverVolumeAsk
	// replaced counts the asks that the newest one replaced, for its
	// line.
	replaced int
	// wake says that next holds an ask, and reported says that the
	// receiver reported a volume or a mute.
	wake     chan struct{}
	reported chan struct{}
}

func newVolumeAsker() *volumeAsker {
	return &volumeAsker{wake: make(chan struct{}, 1), reported: make(chan struct{}, 1)}
}

// put makes ask the next one to send.
func (a *volumeAsker) put(ask ReceiverVolumeAsk) {
	a.mutex.Lock()
	if a.next != nil {
		a.replaced++
	}
	a.next = &ask
	a.mutex.Unlock()
	poke(a.wake)
}

// take answers the next ask and how many asks it replaced, and empties
// the slot.
func (a *volumeAsker) take() (*ReceiverVolumeAsk, int) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	ask, replaced := a.next, a.replaced
	a.next, a.replaced = nil, 0
	return ask, replaced
}

// applyVolumeAsks sends the volume asks one at a time for the life of
// the unit. Each send waits for the receiver to report the volume and
// the mute it was sent, or for pendingVolumeWait, before the next one
// goes out, and the asks that arrive during the wait leave only the
// newest.
func (u *receiverUnit) applyVolumeAsks(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-u.volumeAsks.wake:
		}
		ask, replaced := u.volumeAsks.take()
		if ask != nil {
			u.sendVolumeAsk(ctx, *ask, replaced)
		}
	}
}

// sendVolumeAsk sends one ask as an absolute volume and mute, each only
// when the receiver reports another value. The level is in the
// receiver's own scale, and the driver's steps are a whole number of
// that scale's smallest unit. The media operator holds the level at or
// below spec.volume.max. The operator holds it at or above zero, and
// each driver's SetVolume holds it at or below the top of its own scale.
func (u *receiverUnit) sendVolumeAsk(ctx context.Context, ask ReceiverVolumeAsk, replaced int) {
	resolution := u.driver.VolumeResolution()
	target := max(stepsFromScale(ask.Level, resolution), 0)
	trigger := fmt.Sprintf("status.session.volumeAsk at %s asks %s and mute %s",
		ask.At, volumeWords(equipment.ZoneState{Volume: target}, resolution), onOff(ask.Mute))
	if replaced > 0 {
		trigger += fmt.Sprintf(", the newest of %d asks", replaced+1)
	}
	drainPokes(u.volumeAsks.reported)
	reading := mainZone(u.driver.State())
	var sent []string
	var words []func(equipment.ZoneState, int) string
	began := time.Now()
	if reading.Volume != target {
		volume := volumeWords(equipment.ZoneState{Volume: target}, resolution)
		if err := u.driver.SetVolume(equipment.MainZone, target); err != nil {
			u.log.refused(trigger+"; sent "+volume, err)
		} else {
			sent = append(sent, volume)
			words = append(words, volumeWords)
		}
	}
	if reading.Mute != ask.Mute {
		mute := "mute " + onOff(ask.Mute)
		if err := u.driver.SetMute(equipment.MainZone, ask.Mute); err != nil {
			u.log.refused(trigger+"; sent "+mute, err)
		} else {
			sent = append(sent, mute)
			words = append(words, muteWords)
		}
	}
	if len(sent) == 0 {
		if reading.Volume == target && reading.Mute == ask.Mute {
			u.log.printf("%s; sent nothing, because the receiver reports %s and %s", trigger, volumeWords(reading, resolution), muteWords(reading, 0))
		}
		return
	}
	what := strings.Join(sent, " and ")
	check := mainZoneCheck(u.driver, what, words...)
	u.log.confirm(trigger+"; sent "+what, began, check)
	u.awaitVolume(ctx, check)
}

// awaitVolume waits until check passes, which is when the receiver
// reports what it was sent, or until pendingVolumeWait passes. A
// receiver whose own limit is below the volume never reports it, and
// the wait ends on the timer.
func (u *receiverUnit) awaitVolume(ctx context.Context, check func() (string, bool)) {
	timer := time.NewTimer(pendingVolumeWait)
	defer timer.Stop()
	for {
		if _, reported := check(); reported {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			return
		case <-u.volumeAsks.reported:
		}
	}
}
