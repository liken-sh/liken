package main

// The ensure ask: a press on a controller whose mark names a unit asks
// that unit's receiver for the unit's input, in the receiver's own
// generic vocabulary. The fake broker these read is in bus_test.go.

import (
	"testing"
)

// A press on the controller that drives a unit asks the unit's receiver
// for the unit's input, in the receiver's vocabulary and never in input
// names.
func TestAPressAsksTheUnitsReceiverForItsInput(t *testing.T) {
	media, broker := focusBrokerOperator(t)
	media.focus.setMark(controllerKey("media", "living-room-remote"), "living-room")
	media.ensure.set(playerKey("media", "living-room"), "liken/equipment/living-room-denon/commands")

	media.handleBusMessage(remoteEventsTopic(defaultTopicBase, "media", "living-room-remote"),
		[]byte(`{"key":"KEY_VOLUMEUP","value":1}`))

	published := waitForPublish(t, broker.pubs)
	mustMatch(t, published.topic, "liken/equipment/living-room-denon/commands")
	mustMatch(t, string(published.payload), `{"command":"input.ensure"}`)
}

// A repeat and a release carry the same topic and begin no press, so one
// held control asks once.
func TestARepeatAndAReleaseAskNothing(t *testing.T) {
	media, broker := focusBrokerOperator(t)
	media.focus.setMark(controllerKey("media", "living-room-remote"), "living-room")
	media.ensure.set(playerKey("media", "living-room"), "liken/equipment/living-room-denon/commands")
	topic := remoteEventsTopic(defaultTopicBase, "media", "living-room-remote")

	media.handleBusMessage(topic, []byte(`{"key":"KEY_VOLUMEUP","value":2}`))
	media.handleBusMessage(topic, []byte(`{"key":"KEY_VOLUMEUP","value":0}`))

	mustPublishNothingYet(t, broker, media.bus)
}

// A controller pointed at another room asks nothing: the mark is the
// gate, and it names the unit the presses belong to.
func TestAPressWithAMarkOnAnotherUnitAsksNothing(t *testing.T) {
	media, broker := focusBrokerOperator(t)
	media.focus.setMark(controllerKey("media", "living-room-remote"), "studio")
	media.ensure.set(playerKey("media", "living-room"), "liken/equipment/living-room-denon/commands")

	media.handleBusMessage(remoteEventsTopic(defaultTopicBase, "media", "living-room-remote"),
		[]byte(`{"key":"KEY_VOLUMEUP","value":1}`))

	mustPublishNothingYet(t, broker, media.bus)
}

// A unit wired to no receiver has no commands topic, so its controller's
// presses ask nothing at all.
func TestAPressOnAUnitWithNoReceiverAsksNothing(t *testing.T) {
	media, broker := focusBrokerOperator(t)
	media.focus.setMark(controllerKey("media", "living-room-remote"), "living-room")

	media.handleBusMessage(remoteEventsTopic(defaultTopicBase, "media", "living-room-remote"),
		[]byte(`{"key":"KEY_VOLUMEUP","value":1}`))

	mustPublishNothingYet(t, broker, media.bus)
}

// The pass records the receiver's commands topic for the unit, so a
// press later finds where to ask without a read of its own. A unit that
// stops matching drops the entry.
func TestThePassRecordsTheReceiversCommandsTopic(t *testing.T) {
	cluster := receiverCluster()
	cluster.receivers["living-room-denon"].Spec.CommandsTopic = "liken/equipment/living-room-denon/commands"
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, standingPlays())

	commands, held := media.ensure.commandsFor(playerKey("house", "theater"))
	mustMatch(t, held, true)
	mustMatch(t, commands, "liken/equipment/living-room-denon/commands")

	cluster.receiversAbsent = true
	runPlayers(media, []Player{*housePlayer()}, standingPlays())

	_, stillHeld := media.ensure.commandsFor(playerKey("house", "theater"))
	mustMatch(t, stillHeld, false)
}
