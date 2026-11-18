package main

import (
	"strconv"
	"testing"
)

// askOperator returns an operator over a fake cluster where the den
// Receiver has the session of the Player house/theater, and the sofa
// remote has focus on the theater. It clears the session applies of the
// setup, so a test sees only the applies of its own press.
func askOperator(t *testing.T) (*operator, *fakeCluster) {
	t.Helper()
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	runPlayers(media, []Player{*housePlayer()}, nil)
	media.focus.setMark(controllerKey("house", "sofa"), "theater")
	cluster.sessions = nil
	return media, cluster
}

func press(media *operator, key string, value int) {
	media.handleBusMessage(remoteEventsTopic(defaultTopicBase, "house", "sofa"),
		[]byte(`{"key":"`+key+`","value":`+strconv.Itoa(value)+`}`))
}

// selectInput sets the den Receiver's InputSelected condition and runs
// the reconcile pass that records it.
func selectInput(media *operator, cluster *fakeCluster, status string) {
	receiver := cluster.receivers["den-receiver"]
	receiver.Status.Conditions = append(receiver.Status.Conditions,
		ReceiverCondition{Type: receiverInputSelectedCondition, Status: status})
	runPlayers(media, []Player{*housePlayer()}, nil)
	cluster.sessions = nil
}

// lastInputAsk returns the inputAsk action of the last session apply,
// or "none" when there was no apply or it had no inputAsk.
func lastInputAsk(cluster *fakeCluster) string {
	if len(cluster.sessions) == 0 || cluster.sessions[len(cluster.sessions)-1].session.InputAsk == nil {
		return "none"
	}
	return cluster.sessions[len(cluster.sessions)-1].session.InputAsk.Action
}

// The apply includes the whole session under this operator's field
// manager, so writing the request removes no other session field.
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

// A home press writes a show even while InputSelected is True, because
// the condition says nothing about the TV.
func TestAnEnsureIsWrittenOnlyWhileTheInputIsNotSelected(t *testing.T) {
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

// equipment-operator reads two requests with the same time as one.
func TestEachInputAskHasANewTime(t *testing.T) {
	media, cluster := askOperator(t)

	press(media, "KEY_HOMEPAGE", 1)
	press(media, "KEY_HOMEPAGE", 1)

	mustMatch(t, len(cluster.sessions), 2)
	if cluster.sessions[0].session.InputAsk.At == cluster.sessions[1].session.InputAsk.At {
		t.Errorf("the second inputAsk has the time %s of the first, want a later time", cluster.sessions[0].session.InputAsk.At)
	}
}

func TestAPressThatWritesNoAsk(t *testing.T) {
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
			media.playerReceivers.set(playerKey("house", "theater"), receiverWiring{})
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

// A press reads the Receiver from what the pass recorded, not from the
// API server.
func TestThePassRecordsThePlayersReceiver(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	selectInput(media, cluster, "True")

	recorded, found := media.playerReceivers.receiverFor(playerKey("house", "theater"))
	mustMatch(t, found, true)
	mustMatch(t, recorded, receiverWiring{name: "den-receiver", inputSelected: true})

	cluster.receiversAbsent = true
	runPlayers(media, []Player{*housePlayer()}, nil)

	_, stillRecorded := media.playerReceivers.receiverFor(playerKey("house", "theater"))
	mustMatch(t, stillRecorded, false)
}

func command(media *operator, payload string) {
	media.handleBusMessage(playerCommandsTopic(defaultTopicBase, "house", "theater"), []byte(payload))
}

// No controller focus is needed, because the topic names the Player.
func TestAHomeAskOnTheCommandsTopicWritesAShow(t *testing.T) {
	media, cluster := askOperator(t)
	selectInput(media, cluster, "True")

	command(media, `{"action":"home"}`)

	mustMatch(t, lastInputAsk(cluster), inputShow)
}

// A home action with remote set needs no show, because ensureInput
// already wrote one for that Remote's press.
func TestAMessageOnTheCommandsTopicThatWritesNoAsk(t *testing.T) {
	cases := []struct {
		name    string
		shape   func(*operator)
		payload string
	}{
		{name: "a home action from a Remote's press", payload: `{"action":"home","remote":"house/sofa"}`},
		{name: "an action other than home", payload: `{"action":"play-next"}`},
		{name: "a payload that does not decode", payload: `home`},
		{name: "no receiver", payload: `{"action":"home"}`, shape: func(media *operator) {
			media.playerReceivers.set(playerKey("house", "theater"), receiverWiring{})
		}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			media, cluster := askOperator(t)
			if each.shape != nil {
				each.shape(media)
			}

			command(media, each.payload)

			mustMatch(t, lastInputAsk(cluster), "none")
		})
	}
}
