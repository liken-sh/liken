package main

// A held remote key sends presses faster than a Denon answers them. The
// session steps each press from the volume it last sent, and it holds
// its report back until the receiver reaches that volume.

import (
	"testing"
	"testing/synctest"
)

// holdEchoes makes the receiver apply each volume it is sent and report
// none of them until releaseEchoes.
func (f *fakeDenon) holdEchoes() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.holdingEchoes = true
}

// releaseEchoes sends every report the receiver held, in the order it
// took the commands, and reports at once from then on.
func (f *fakeDenon) releaseEchoes() {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.holdingEchoes = false
	for _, line := range f.heldEchoes {
		f.send(line)
	}
	f.heldEchoes = nil
}

// press publishes the level a sidecar computes for one press.
func press(t *testing.T, broker *fakeBroker, level int) {
	t.Helper()
	payload, err := marshalVolumeState(volumeState{Level: level})
	mustSucceed(t, err)
	broker.push(testVolumeTopic, payload)
}

func TestPressesTheReceiverHasNotAnsweredEachMoveOneStep(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, broker, _ := listeningWith(t, ReceiverVolume{Max: 69.5, Step: 0.5}, 72)
		h.equipment.holdEchoes()

		press(t, broker, 67)
		h.equipment.waitForCommands(t, "MV495")
		press(t, broker, 62)
		h.equipment.waitForCommands(t, "MV49")
		press(t, broker, 57)
		h.equipment.waitForCommands(t, "MV485")
	})
}

// The receiver's reports of the first presses arrive after the later
// presses were sent. Publishing them would put a level above the last
// press on the topic, and the sidecar's next press would count from it.
func TestTheSessionReportsOnceTheReceiverReachesItsLastPress(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, broker, _ := listeningWith(t, ReceiverVolume{Max: 69.5, Step: 0.5}, 72)
		h.equipment.holdEchoes()
		press(t, broker, 67)
		h.equipment.waitForCommands(t, "MV495")
		press(t, broker, 62)
		h.equipment.waitForCommands(t, "MV49")
		press(t, broker, 57)
		h.equipment.waitForCommands(t, "MV485")
		broker.refuseTopic(t, testVolumeTopic, quietPeriod)

		h.equipment.releaseEchoes()

		mustMatch(t, positionOf(t, broker.waitForTopic(t, testVolumeTopic)), volumeState{Level: 70})
	})
}

// A receiver that never reports the volume it was sent stops holding
// the session's report after pendingVolumeWait. The session then
// publishes what the receiver reports and steps the next press from it.
func TestAVolumeTheReceiverNeverReportsIsGivenUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, broker, _ := listeningWith(t, ReceiverVolume{Max: 69.5, Step: 0.5}, 72)
		h.equipment.holdEchoes()
		press(t, broker, 67)
		h.equipment.waitForCommands(t, "MV495")

		mustMatch(t, positionOf(t, broker.waitForTopic(t, testVolumeTopic)), volumeState{Level: 72})

		press(t, broker, 67)
		h.equipment.waitForCommands(t, "MV495")
	})
}
