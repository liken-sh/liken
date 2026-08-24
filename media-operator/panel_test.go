package main

// These tests cover the panel desk and the fold: what the idle
// sidecar published on the bus reaches the Player's status, and a
// unit no sidecar reported carries no panel at all.

import "testing"

// panelPlayer is one Player that drives a screen and holds the
// panel's wire.
func panelPlayer() Player {
	return Player{
		Metadata: ObjectMeta{Name: "theater", Namespace: "house"},
		Spec: PlayerSpec{
			Display: &PlayerDevice{Class: "display-output"},
			Control: &PlayerDevice{Class: "display-control"},
		},
	}
}

// A retained panel message reaches the operator on the panel topic,
// and the next pass folds it into the Player's status, so a person
// reads in kubectl what the sidecar actuated.
func TestABusPanelStateReachesThePlayerStatus(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	player := panelPlayer()

	media.handleBusMessage(playerPanelTopic(defaultTopicBase, "house", "theater"),
		[]byte(`{"state":"BacklightOff"}`))
	media.reconcilePlayers([]Player{player}, nil, "", nil)

	mustMatch(t, cluster.players["theater"].Status.Panel, panelBacklightOff)
}

// The wake reaches the status the same way, so the panel reads On
// again on the pass after the sidecar published it.
func TestABusPanelWakeReachesThePlayerStatus(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	player := panelPlayer()
	topic := playerPanelTopic(defaultTopicBase, "house", "theater")

	media.handleBusMessage(topic, []byte(`{"state":"Off"}`))
	media.reconcilePlayers([]Player{player}, nil, "", nil)
	mustMatch(t, cluster.players["theater"].Status.Panel, panelOff)

	media.handleBusMessage(topic, []byte(`{"state":"On"}`))
	media.reconcilePlayers([]Player{*cluster.players["theater"]}, nil, "", nil)

	mustMatch(t, cluster.players["theater"].Status.Panel, panelOn)
}

// A unit no sidecar reported carries no panel, because a Player that
// states no control device darkens nothing and reports nothing.
func TestAPlayerWithNoPanelMessageCarriesNoPanel(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.reconcilePlayers([]Player{panelPlayer()}, nil, "", nil)

	mustMatch(t, cluster.players["theater"].Status.Panel, "")
}

// An empty payload is a cleared retained value and not a live
// signal, so the desk holds the state it had.
func TestAClearedPanelTopicLeavesTheStateAlone(t *testing.T) {
	cluster := newFakeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	topic := playerPanelTopic(defaultTopicBase, "house", "theater")

	media.handleBusMessage(topic, []byte(`{"state":"BacklightOff"}`))
	media.handleBusMessage(topic, nil)
	media.handleBusMessage(topic, []byte("not json"))

	mustMatch(t, media.panels.stateFor(playerKey("house", "theater")), panelBacklightOff)
}

// The desk wakes the loop on a state it did not hold and on a
// change, and not when the retained topic delivers the same message
// again.
func TestThePanelDeskWakesOnlyOnChange(t *testing.T) {
	wake := make(chan struct{}, 1)
	desk := newPanelDesk(wake)
	key := playerKey("house", "theater")

	desk.setState(key, panelBacklightOff)
	mustMatch(t, len(wake), 1)

	<-wake
	desk.setState(key, panelBacklightOff)
	mustMatch(t, len(wake), 0)

	desk.setState(key, panelOn)
	mustMatch(t, len(wake), 1)
}

// The desk shrinks to the units the cluster still holds, so a
// long-running operator keeps no key for a deleted Player.
func TestThePanelDeskDropsADeletedPlayer(t *testing.T) {
	desk := newPanelDesk(make(chan struct{}, 1))
	desk.setState(playerKey("house", "theater"), panelOff)
	desk.setState(playerKey("house", "studio"), panelOn)

	desk.retain(map[string]bool{playerKey("house", "studio"): true})

	mustMatch(t, desk.stateFor(playerKey("house", "theater")), "")
	mustMatch(t, desk.stateFor(playerKey("house", "studio")), panelOn)
}
