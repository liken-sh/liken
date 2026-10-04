package main

// The volume a session sent and the receiver has not reported yet.
//
// A held remote key sends a press about every 42 ms, and a Denon
// reports a volume tens of milliseconds after it takes it. The driver
// records a volume only when the receiver reports it, so a press that
// stepped from the driver's state would compute the same volume as the
// press before it, and the receiver would move one step for several
// presses. The session steps each press from the volume it last sent.
//
// The receiver's reports of the earlier presses arrive after the later
// presses went out. Each of them is above the last press on a held
// volume-down key, and the sidecar counts its next press from what the
// topic carries, so the session publishes no report until the receiver
// reports the volume it was last sent.

import (
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// pendingVolumeWait is how long the session waits for the receiver to
// report the volume it was sent. A Denon reports within a fraction of a
// second. A receiver that never reports the volume, such as one whose
// own limit is below it, publishes its real position after this wait,
// and the next press steps from that position.
const pendingVolumeWait = time.Second

// pendingVolume is the volume the session last sent, in the driver's
// steps. The session's mutex guards it. generation tells an expiry
// that fires late from the expiry of the volume pending now.
type pendingVolume struct {
	steps      int
	set        bool
	generation int
	timer      *time.Timer
}

// position answers where the next press steps from: the volume the
// session last sent while the receiver has not reported it, and the
// receiver's report otherwise.
func (s *session) position(reading equipment.ZoneState) equipment.ZoneState {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.pending.set {
		reading.Volume = s.pending.steps
	}
	return reading
}

// aim records a volume the session is about to send, and starts the
// wait for the receiver to report it.
func (s *session) aim(steps int) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.pending.timer != nil {
		s.pending.timer.Stop()
	}
	s.pending.generation++
	generation := s.pending.generation
	s.pending.steps, s.pending.set = steps, true
	s.pending.timer = time.AfterFunc(pendingVolumeWait, func() { s.giveUp(generation) })
}

// settle answers whether a report can go on the topic: no volume is
// pending, or the report is the volume pending, which ends the wait.
func (s *session) settle(reading equipment.ZoneState) bool {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if !s.pending.set {
		return true
	}
	if reading.Volume != s.pending.steps {
		return false
	}
	s.clearPending()
	return true
}

// release ends the wait for a volume the driver refused to send.
func (s *session) release() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.clearPending()
}

// giveUp ends the wait for a volume the receiver never reported, and
// publishes where the receiver reports it is.
func (s *session) giveUp(generation int) {
	if s.ctx.Err() != nil {
		return
	}
	s.mutex.Lock()
	current := s.pending.set && s.pending.generation == generation
	if current {
		s.clearPending()
	}
	s.mutex.Unlock()
	if current {
		s.report(mainZone(s.driver.State()))
	}
}

// clearPending forgets the pending volume. The caller holds the mutex.
func (s *session) clearPending() {
	if s.pending.timer != nil {
		s.pending.timer.Stop()
	}
	s.pending.set, s.pending.timer = false, nil
}
