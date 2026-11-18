package main

// media-operator writes a volume, a power, or an input request into a
// Receiver's status.session when a person presses a key. Each request
// has an `at` timestamp, and a new `at` marks a new request. The
// operator sends each request once and does not hold the receiver at
// that value, so a person can still change it with the receiver's own
// remote. The session's active and awake flags follow the same rule.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liken-sh/liken/equipment-operator/equipment"
)

// seenAsks records the `at` of the latest request of each kind that the
// unit has read. It belongs to the unit and not to the session, so a
// session that replaces another, as for a new input, does not send the
// requests again. Only the pass goroutine reads and writes it.
type seenAsks struct {
	volume, power, input string
}

// takeAsks sends each request whose `at` differs from the one the unit
// last read. On the first pass after the operator starts, adopting is
// true, and takeAsks records each `at` without sending. A person may
// have changed the receiver since media-operator wrote the request, and
// the previous operator process may have sent it already. A power or
// input request that arrives while the unit has no session is recorded
// and not sent.
func (u *receiverUnit) takeAsks(spec *ReceiverSession, adopting bool) {
	if spec == nil {
		return
	}
	u.mutex.Lock()
	current := u.session
	u.mutex.Unlock()
	if ask := spec.VolumeAsk; ask != nil && ask.At != u.seen.volume {
		u.seen.volume = ask.At
		if !adopting {
			u.volumeAsks.put(*ask)
		}
	}
	if ask := spec.PowerAsk; ask != nil && ask.At != u.seen.power {
		u.seen.power = ask.At
		if !adopting && current != nil {
			source := "status.session.powerAsk at " + ask.At
			goWork(current.ctx, func() { current.power(ask.Action, source) })
		}
	}
	if ask := spec.InputAsk; ask != nil && ask.At != u.seen.input {
		u.seen.input = ask.At
		if !adopting && current != nil {
			current.inputAsk(*ask)
		}
	}
}

// carryAsks copies the requests that the replaced unit had read, so the
// new unit does not send them again.
func (u *receiverUnit) carryAsks(replaced *receiverUnit) {
	u.seen = replaced.seen
}

// inputAsk runs ensure or show (session_ensure.go) for an input request,
// and ignores any other action.
func (s *session) inputAsk(ask ReceiverInputAsk) {
	trigger := fmt.Sprintf("status.session.inputAsk at %s requests %s", ask.At, ask.Action)
	switch ask.Action {
	case "ensure":
		s.ensure(trigger)
	case "show":
		s.show(trigger)
	}
}

// pendingVolumeWait is the longest that the operator waits for the
// receiver to report a volume it was sent before it sends the next
// volume request. The wait needs a limit because a receiver whose own
// volume limit is below the requested level never reports that level.
const pendingVolumeWait = time.Second

// volumeAsker holds the newest volume request that has not been sent.
// Holding a volume key writes a request for each key repeat. If the
// operator sent every request, the receiver would step through each
// level and fall behind the key. So a new request replaces an unsent
// one, and the receiver goes straight to the newest level.
type volumeAsker struct {
	mutex sync.Mutex
	next  *ReceiverVolumeAsk
	// replaced counts the requests that next replaced, for the log
	// line.
	replaced int
	// wake receives a value when next holds a request, and reported
	// when the receiver reports a volume or a mute.
	wake     chan struct{}
	reported chan struct{}
}

func newVolumeAsker() *volumeAsker {
	return &volumeAsker{wake: make(chan struct{}, 1), reported: make(chan struct{}, 1)}
}

// put makes ask the next request to send, in place of any unsent one.
func (a *volumeAsker) put(ask ReceiverVolumeAsk) {
	a.mutex.Lock()
	if a.next != nil {
		a.replaced++
	}
	a.next = &ask
	a.mutex.Unlock()
	sendWake(a.wake)
}

func (a *volumeAsker) take() (*ReceiverVolumeAsk, int) {
	a.mutex.Lock()
	defer a.mutex.Unlock()
	ask, replaced := a.next, a.replaced
	a.next, a.replaced = nil, 0
	return ask, replaced
}

// applyVolumeAsks sends the volume requests one at a time until ctx
// ends. After each send it waits in awaitVolume, and of the requests
// that arrive during the wait it sends only the newest.
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

// sendVolumeAsk sets the receiver to the volume and mute in the
// request, and sends each one only when the receiver reports a
// different value. ask.Level is in the receiver's own scale, and
// stepsFromScale converts it to the nearest whole step of the driver.
// media-operator keeps the level at or below spec.volume.max,
// sendVolumeAsk raises a negative level to zero, and each driver's
// SetVolume lowers a level above the top of its scale to the top.
func (u *receiverUnit) sendVolumeAsk(ctx context.Context, ask ReceiverVolumeAsk, replaced int) {
	resolution := u.driver.VolumeResolution()
	target := max(stepsFromScale(ask.Level, resolution), 0)
	trigger := fmt.Sprintf("status.session.volumeAsk at %s requests %s and mute %s",
		ask.At, volumeWords(equipment.ZoneState{Volume: target}, resolution), onOff(ask.Mute))
	if replaced > 0 {
		trigger += fmt.Sprintf(", the newest of %d requests", replaced+1)
	}
	drainWakes(u.volumeAsks.reported)
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

// awaitVolume waits until check passes, which means that the receiver
// reports what it was sent, or until pendingVolumeWait passes.
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
