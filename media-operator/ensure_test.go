package main

// The input asks: a press on a controller whose mark names a unit
// writes an inputAsk into the session on the unit's Receiver. The fake
// API server these read is in operate_test.go.

import (
	"strconv"
	"testing"
)

// askOperator is an operator over a fake cluster whose den Receiver
// holds the house theater's session, with the sofa remote's mark on the
// theater. The session apply of the pass that set it up is dropped from
// the record, so a test reads the applies its own press made.
func askOperator(t *testing.T) (*operator, *fakeCluster) {
	t.Helper()
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	runPlayers(media, []Player{*housePlayer()}, nil)
	media.focus.setMark(controllerKey("house", "sofa"), "theater")
	cluster.sessions = nil
	return media, cluster
}

// press hands the operator one event from the sofa remote.
func press(media *operator, key string, value int) {
	media.handleBusMessage(remoteEventsTopic(defaultTopicBase, "house", "sofa"),
		[]byte(`{"key":"`+key+`","value":`+strconv.Itoa(value)+`}`))
}

// selectInput sets the den Receiver's InputSelected condition and runs
// the pass that reads it.
func selectInput(media *operator, cluster *fakeCluster, status string) {
	receiver := cluster.receivers["den-receiver"]
	receiver.Status.Conditions = append(receiver.Status.Conditions,
		ReceiverCondition{Type: receiverInputSelectedCondition, Status: status})
	runPlayers(media, []Player{*housePlayer()}, nil)
	cluster.sessions = nil
}

// lastInputAsk reads the action of the input ask the last apply carried,
// or none.
func lastInputAsk(cluster *fakeCluster) string {
	if len(cluster.sessions) == 0 || cluster.sessions[len(cluster.sessions)-1].session.InputAsk == nil {
		return "none"
	}
	return cluster.sessions[len(cluster.sessions)-1].session.InputAsk.Action
}

// A press on the controller that drives a unit asks the unit's receiver
// for the unit's input, in the receiver's vocabulary and never in input
// names. The apply states the whole session beside the ask, under this
// operator's field manager, so the apply removes none of the session.
func TestAPressWritesAnEnsureIntoTheSession(t *testing.T) {
	media, cluster := askOperator(t)

	press(media, "KEY_UP", 1)

	mustMatch(t, len(cluster.sessions), 1)
	applied := cluster.sessions[0]
	mustMatch(t, applied.manager, applyFieldManager)
	mustMatch(t, applied.session.Player, "house/theater")
	mustMatch(t, applied.session.Input, "GAME")
	mustMatch(t, applied.session.Awake, true)
	mustMatch(t, applied.session.InputAsk.Action, inputEnsure)
}

// While the receiver reports the session's input, an ensure would ask
// for nothing, so a press writes nothing. A home press asks for the TV
// too, which the condition says nothing about, so it is written.
func TestAnEnsureWaitsForTheInputToBeLost(t *testing.T) {
	cases := []struct {
		name     string
		selected string
		key      string
		want     string
	}{
		{name: "selected", selected: "True", key: "KEY_UP", want: "none"},
		{name: "not selected", selected: "False", key: "KEY_UP", want: inputEnsure},
		{name: "unknown", selected: "Unknown", key: "KEY_UP", want: inputEnsure},
		{name: "home while selected", selected: "True", key: "KEY_HOMEPAGE", want: inputShow},
		{name: "the other home key", selected: "True", key: "KEY_WWW", want: inputShow},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			media, cluster := askOperator(t)
			selectInput(media, cluster, each.selected)

			press(media, each.key, 1)

			mustMatch(t, lastInputAsk(cluster), each.want)
		})
	}
}

// Each ask carries a new time, so the equipment operator reads two
// presses as two asks.
func TestEachInputAskCarriesANewTime(t *testing.T) {
	media, cluster := askOperator(t)

	press(media, "KEY_HOMEPAGE", 1)
	press(media, "KEY_HOMEPAGE", 1)

	mustMatch(t, len(cluster.sessions), 2)
	if cluster.sessions[0].session.InputAsk.At == cluster.sessions[1].session.InputAsk.At {
		t.Errorf("two asks carry one time, %s", cluster.sessions[0].session.InputAsk.At)
	}
}

// A press asks nothing when it is a power key, a repeat or a release, a
// press on a controller pointed at another unit, or a press on a unit
// wired to no receiver. The power key is the room's toggle, and an
// input ask beside it would make the receiver select the input, and
// power it on, while the toggle puts the room to standby.
func TestAPressThatAsksNothing(t *testing.T) {
	cases := []struct {
		name  string
		shape func(*operator)
		key   string
		value int
	}{
		{name: "KEY_POWER", key: "KEY_POWER", value: 1},
		{name: "KEY_POWER2", key: "KEY_POWER2", value: 1},
		{name: "KEY_SLEEP", key: "KEY_SLEEP", value: 1},
		{name: "KEY_WAKEUP", key: "KEY_WAKEUP", value: 1},
		{name: "a repeat", key: "KEY_UP", value: 2},
		{name: "a release", key: "KEY_UP", value: 0},
		{name: "a mark on another unit", key: "KEY_UP", value: 1, shape: func(media *operator) {
			media.focus.setMark(controllerKey("house", "sofa"), "studio")
		}},
		{name: "no receiver", key: "KEY_UP", value: 1, shape: func(media *operator) {
			media.ensure.set(playerKey("house", "theater"), unitReceiver{})
		}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			media, cluster := askOperator(t)
			if each.shape != nil {
				each.shape(media)
			}

			press(media, each.key, each.value)

			mustMatch(t, len(cluster.sessions), 0)
		})
	}
}

// The pass records the unit's Receiver and its InputSelected condition,
// so a press later finds where to ask without a read of its own. A unit
// that stops matching drops the entry.
func TestThePassRecordsTheUnitsReceiver(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	selectInput(media, cluster, "True")

	held, found := media.ensure.receiverFor(playerKey("house", "theater"))
	mustMatch(t, found, true)
	mustMatch(t, held, unitReceiver{name: "den-receiver", inputSelected: true})

	cluster.receiversAbsent = true
	runPlayers(media, []Player{*housePlayer()}, nil)

	_, stillHeld := media.ensure.receiverFor(playerKey("house", "theater"))
	mustMatch(t, stillHeld, false)
}
