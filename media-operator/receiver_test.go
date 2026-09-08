package main

// These tests cover the media layer's half of the Receiver resource:
// the match from a unit's screen to an input, the session a matched
// unit holds on the equipment, the active flag a standing Play moves,
// and the lift.

import (
	"strconv"
	"strings"
	"testing"
)

// The equipment the house unit's cable lands on. The first input
// carries another machine's cable, so a match proves both values are
// read.
func houseReceiver() *Receiver {
	return &Receiver{
		Metadata: ObjectMeta{Name: "living-room-denon"},
		Spec: ReceiverSpec{Inputs: []ReceiverInput{
			{Name: "CBL/SAT", Machine: "nuc6", Monitor: testMonitor},
			{Name: "GAME", Machine: testNode, Monitor: testMonitor},
		}},
		Status: ReceiverStatus{
			Power: "on",
			Input: "GAME",
			Conditions: []ReceiverCondition{
				{Type: receiverReachableCondition, Status: "True", Reason: "Connected"},
			},
		},
	}
}

// A cluster whose theater unit draws on a screen wired into one
// Receiver input.
func receiverCluster() *fakeCluster {
	cluster := screenCluster()
	cluster.receivers["living-room-denon"] = houseReceiver()
	return cluster
}

// An input matches on the machine and the monitor id together, because
// one receiver forwards one EDID on every input.
func TestAnInputMatchesTheMachineAndTheMonitorTogether(t *testing.T) {
	cases := []struct {
		name    string
		node    string
		monitor string
		want    string
	}{
		{name: "both values name the input", node: testNode, monitor: testMonitor, want: "GAME"},
		{name: "another machine on the same monitor", node: "nuc7", monitor: testMonitor},
		{name: "the same machine on another monitor", node: testNode, monitor: "HDMI-1"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			input, matched := matchReceiverInput(houseReceiver(), each.node, each.monitor)

			mustMatch(t, matched, each.want != "")
			mustMatch(t, input, each.want)
		})
	}
}

// An entry with no input name names no input, so a half-written
// Receiver matches nothing.
func TestAnInputWithNoNameMatchesNothing(t *testing.T) {
	receiver := houseReceiver()
	receiver.Spec.Inputs = []ReceiverInput{{Machine: testNode, Monitor: testMonitor}}

	_, matched := matchReceiverInput(receiver, testNode, testMonitor)

	mustMatch(t, matched, false)
}

// The Receivers are listed at most once a pass, however many units ask,
// and a unit with no resolved screen asks nothing.
func TestTheReceiversAreListedOnceAPass(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	lookup := media.receiverLookup()

	receiver, input, matched := lookup.matchFor(testNode, testMonitor)

	mustMatch(t, matched, true)
	mustMatch(t, receiver.Metadata.Name, "living-room-denon")
	mustMatch(t, input, "GAME")

	_, _, matched = lookup.matchFor(testNode, testMonitor)

	mustMatch(t, matched, true)
	mustMatch(t, countPathRequests(cluster.requests, "GET "+receiversPath), 1)
}

// A screen this pass could not resolve names no equipment, and the
// lookup then makes no request at all.
func TestAnUnresolvedScreenMatchesNoReceiver(t *testing.T) {
	cases := []struct {
		name    string
		node    string
		monitor string
	}{
		{name: "no machine", monitor: testMonitor},
		{name: "no monitor id", node: testNode},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := receiverCluster()
			media := testOperator(t, cluster, make(chan struct{}, 1))

			_, _, matched := media.receiverLookup().matchFor(each.node, each.monitor)

			mustMatch(t, matched, false)
			mustMatch(t, countPathRequests(cluster.requests, "GET "+receiversPath), 0)
		})
	}
}

// A cluster that runs no equipment operator serves no Receiver
// collection, and a list that fails is the same answer: no match, and
// the unit plays as it does today.
func TestAClusterWithNoReceiversMatchesNothing(t *testing.T) {
	cases := []struct {
		name  string
		shape func(*fakeCluster)
	}{
		{name: "the collection is absent", shape: func(c *fakeCluster) { c.receiversAbsent = true }},
		{name: "the list fails", shape: func(c *fakeCluster) { c.fails[receiversPath] = true }},
		{name: "the cluster holds no receiver", shape: func(c *fakeCluster) { c.receivers = map[string]*Receiver{} }},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := receiverCluster()
			each.shape(cluster)
			media := testOperator(t, cluster, make(chan struct{}, 1))

			_, _, matched := media.receiverLookup().matchFor(testNode, testMonitor)

			mustMatch(t, matched, false)
		})
	}
}

// The Reachable condition folds to its status word, and a Receiver that
// carries no such condition folds to nothing.
func TestTheReachableConditionFoldsToOneWord(t *testing.T) {
	cases := []struct {
		name       string
		conditions []ReceiverCondition
		want       string
	}{
		{name: "no conditions", want: ""},
		{
			name:       "another condition alone",
			conditions: []ReceiverCondition{{Type: "Paired", Status: "True"}},
		},
		{
			name:       "a round trip answered",
			conditions: []ReceiverCondition{{Type: receiverReachableCondition, Status: "True"}},
			want:       "True",
		},
		{
			name:       "the socket is half open",
			conditions: []ReceiverCondition{{Type: receiverReachableCondition, Status: "False"}},
			want:       "False",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			receiver := houseReceiver()
			receiver.Status.Conditions = each.conditions

			mustMatch(t, receiverReachable(receiver), each.want)
		})
	}
}

// A unit holds a standing run while a Play names it and that Play has
// not finished, which is the condition that keeps its playback pod.
func TestAStandingRunIsAPlayThatHasNotFinished(t *testing.T) {
	otherPlayer := housePlay("https://nas/film.mkv")
	otherPlayer.Spec.Players = []string{"kitchen"}
	otherNamespace := housePlay("https://nas/film.mkv")
	otherNamespace.Metadata.Namespace = "guest"

	cases := []struct {
		name  string
		phase string
		plays []Play
		want  bool
	}{
		{name: "no play names the unit", want: false},
		{name: "a play with no status yet", phase: "", want: true},
		{name: "a pending play", phase: phasePending, want: true},
		{name: "a running play", phase: phaseRunning, want: true},
		{name: "a failed play keeps its pod", phase: phaseFailed, want: true},
		{name: "a finished play is over", phase: phaseFinished, want: false},
		{name: "another unit's play", plays: []Play{*otherPlayer}, want: false},
		{name: "another namespace's play", plays: []Play{*otherNamespace}, want: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			plays := each.plays
			if plays == nil && each.name != "no play names the unit" {
				play := housePlay("https://nas/film.mkv")
				play.Status.Phase = each.phase
				plays = []Play{*play}
			}

			mustMatch(t, playerHasStandingPlay(housePlayer(), plays), each.want)
		})
	}
}

// The Player status names the equipment its cable lands on and folds
// the Receiver's Reachable condition.
//
// An idle unit holds a session too, with the active flag false, so the
// equipment owns the level while nothing plays and a volume press at
// the idle screen moves the room.
func TestAnIdleUnitReportsItsReceiverAndHoldsAnIdleSession(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, nil)

	status := cluster.players["theater"].Status.Receiver
	if status == nil {
		t.Fatal("the status named no receiver")
	}
	mustMatch(t, status.Name, "living-room-denon")
	mustMatch(t, status.Input, "GAME")
	mustMatch(t, status.Reachable, "True")
	mustMatch(t, len(cluster.sessions), 1)
	mustMatch(t, *cluster.sessions[0].session, ReceiverSession{
		Player:      "house/theater",
		Input:       "GAME",
		Awake:       true,
		VolumeTopic: playerVolumeTopic(defaultTopicBase, "house", "theater"),
	})
}

// A unit whose screen no Receiver names reports no receiver block. That
// is the ordinary unit, wired straight into its panel.
func TestAUnitWiredToNoReceiverReportsNoBlock(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, nil)

	if status := cluster.players["theater"].Status.Receiver; status != nil {
		t.Errorf("the status named receiver %+v", status)
	}
}

// runPlayers runs one pass over the units, with the two lookups dropped
// the way a pass drops them, so a cluster edited between passes is read
// again.
func runPlayers(media *operator, players []Player, plays []Play) {
	media.screenCache, media.receiverCache = nil, nil
	media.reconcilePlayers(players, plays, "", nil)
}

// standingPlays returns one running Play for the house unit.
func standingPlays() []Play {
	play := housePlay("https://nas/film.mkv")
	play.Status.Phase = phaseRunning
	return []Play{*play}
}

// While a Play stands the operator applies the session under its own
// field manager, and it applies it once. Power and input are one-shots
// the equipment answers once.
func TestAStandingPlayHoldsOneSessionOnTheReceiver(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, standingPlays())
	runPlayers(media, []Player{*housePlayer()}, standingPlays())

	mustMatch(t, len(cluster.sessions), 1)
	mustMatch(t, cluster.sessions[0].name, "living-room-denon")
	mustMatch(t, cluster.sessions[0].manager, applyFieldManager)
	mustMatch(t, *cluster.sessions[0].session, ReceiverSession{
		Player:      "house/theater",
		Input:       "GAME",
		Active:      true,
		Awake:       true,
		VolumeTopic: playerVolumeTopic(defaultTopicBase, "house", "theater"),
	})
	mustMatch(t, media.receiverSessions[playerKey("house", "theater")].receiver, "living-room-denon")
}

// A Play that starts flips the active flag, and the end of that Play
// flips it back. Neither edge is a lift: the session stands, so the
// room keeps its input and its level owner across the film.
func TestAPlayMovesTheActiveFlagAndNeitherEdgeLifts(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, nil)
	runPlayers(media, []Player{*housePlayer()}, standingPlays())
	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, appliedActive(cluster), "false, true, false")
	mustMatch(t, media.receiverSessions[playerKey("house", "theater")].receiver, "living-room-denon")
	if cluster.receivers["living-room-denon"].Spec.Session == nil {
		t.Error("the receiver holds no session")
	}
}

// appliedActive reads the active flag of every session apply the passes
// made, in order, and reads a lift as the word.
func appliedActive(cluster *fakeCluster) string {
	flags := make([]string, len(cluster.sessions))
	for index, applied := range cluster.sessions {
		flags[index] = "lift"
		if applied.session != nil {
			flags[index] = strconv.FormatBool(applied.session.Active)
		}
	}
	return strings.Join(flags, ", ")
}

// statePanelDesire hands the operator one panel desire off the bus, the
// way the idle client publishes it.
func statePanelDesire(media *operator, desire string) {
	media.handleBusMessage(playerPanelTopic(defaultTopicBase, "house", "theater"),
		[]byte(`{"desire":"`+desire+`"}`))
}

// The awake flag follows the unit's panel desire, and a unit that
// published no desire at all is awake.
func TestThePanelDesireMovesTheAwakeFlag(t *testing.T) {
	cases := []struct {
		name   string
		desire string
		want   bool
	}{
		{name: "no desire published", want: true},
		{name: "the panel is on", desire: panelDesireOn, want: true},
		{name: "the panel is off", desire: panelDesireOff, want: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := receiverCluster()
			media := testOperator(t, cluster, make(chan struct{}, 1))
			if each.desire != "" {
				statePanelDesire(media, each.desire)
			}

			runPlayers(media, []Player{*housePlayer()}, nil)

			mustMatch(t, len(cluster.sessions), 1)
			mustMatch(t, *cluster.sessions[0].session, ReceiverSession{
				Player:      "house/theater",
				Input:       "GAME",
				Awake:       each.want,
				VolumeTopic: playerVolumeTopic(defaultTopicBase, "house", "theater"),
			})
		})
	}
}

// The press that turns the panel on is applied on the next pass,
// because the tracking compares the whole session and only the awake
// flag changed.
func TestThePressThatWakesThePanelAppliesTheSessionAgain(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	statePanelDesire(media, panelDesireOff)
	runPlayers(media, []Player{*housePlayer()}, nil)
	statePanelDesire(media, panelDesireOn)
	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, appliedAwake(cluster), "false, true")
}

// appliedAwake reads the awake flag of every session apply the passes
// made, in order, and reads a lift as the word.
func appliedAwake(cluster *fakeCluster) string {
	flags := make([]string, len(cluster.sessions))
	for index, applied := range cluster.sessions {
		flags[index] = "lift"
		if applied.session != nil {
			flags[index] = strconv.FormatBool(applied.session.Awake)
		}
	}
	return strings.Join(flags, ", ")
}

// A Player that is gone takes its session with it, the same as a
// deleted unit's dark panel takes a lift.
func TestAUnitThatIsGoneLiftsItsSession(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, nil)
	runPlayers(media, nil, nil)

	mustMatch(t, len(media.receiverSessions), 0)
	if cluster.receivers["living-room-denon"].Spec.Session != nil {
		t.Error("the receiver still holds a session")
	}
}

// A lift the API server refuses keeps the entry, so the next pass
// writes the Receiver again.
func TestAFailedLiftRetriesOnTheNextPass(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	key := playerKey("house", "theater")

	runPlayers(media, []Player{*housePlayer()}, standingPlays())
	cluster.sessionsFail = true
	runPlayers(media, nil, nil)

	mustMatch(t, media.receiverSessions[key].receiver, "living-room-denon")

	cluster.sessionsFail = false
	runPlayers(media, nil, nil)

	mustMatch(t, len(media.receiverSessions), 0)
	if cluster.receivers["living-room-denon"].Spec.Session != nil {
		t.Error("the receiver still holds a session")
	}
}

// A Receiver that is gone carries no session, so the lift it refuses
// has already landed and the entry goes.
func TestALiftOnAReceiverThatIsGoneDropsTheEntry(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, standingPlays())
	delete(cluster.receivers, "living-room-denon")
	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, len(media.receiverSessions), 0)
}

// An apply the API server refuses records nothing, so the next pass
// sends the session again instead of counting it applied.
func TestAFailedSessionApplyIsSentAgain(t *testing.T) {
	cluster := receiverCluster()
	cluster.sessionsFail = true
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, standingPlays())

	mustMatch(t, len(media.receiverSessions), 0)

	cluster.sessionsFail = false
	runPlayers(media, []Player{*housePlayer()}, standingPlays())

	mustMatch(t, len(cluster.sessions), 2)
	mustMatch(t, media.receiverSessions[playerKey("house", "theater")].receiver, "living-room-denon")
}

// A session that changed is applied again, because the input the unit
// plays through is what the equipment is told to select.
func TestASessionThatChangedIsAppliedAgain(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, standingPlays())
	cluster.receivers["living-room-denon"].Spec.Inputs[1].Name = "MPLAY"
	runPlayers(media, []Player{*housePlayer()}, standingPlays())

	mustMatch(t, len(cluster.sessions), 2)
	mustMatch(t, cluster.sessions[1].session.Input, "MPLAY")
}

// The creating pass applies the session before it creates the pod, so
// the equipment is awake and on the right input by the time mpv draws
// its first frame.
func TestTheSessionReachesTheReceiverBeforeThePodIsCreated(t *testing.T) {
	cluster := receiverCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	applied := indexOfRequest(cluster.requests, "PATCH "+receiversPath+"/living-room-denon")
	created := indexOfRequest(cluster.requests, "POST /api/v1/namespaces/house/pods")
	if applied < 0 || created < 0 {
		t.Fatalf("the pass made %v", cluster.requests)
	}
	if applied > created {
		t.Errorf("the session was applied after the pod: %v", cluster.requests)
	}
}

// countPathRequests counts the requests a pass made against one method
// and path.
func countPathRequests(requests []string, want string) int {
	count := 0
	for _, request := range requests {
		if request == want {
			count++
		}
	}
	return count
}

// indexOfRequest is where one request sits in the order the pass made
// them, and -1 when the pass never made it.
func indexOfRequest(requests []string, want string) int {
	for index, request := range requests {
		if request == want {
			return index
		}
	}
	return -1
}

// A unit whose screen moves to another Receiver lifts the session on the
// old Receiver before it applies the session on the new one, and a lift
// that fails leaves the new Receiver alone until the next pass.
func TestAUnitThatMovesToAnotherReceiverLiftsTheOldSessionFirst(t *testing.T) {
	cases := []struct {
		name        string
		liftFails   bool
		wantApplies string
		wantTracked string
	}{
		{
			name:        "the move lands",
			wantApplies: "living-room-denon: GAME, living-room-denon: lift, den-denon: MPLAY",
			wantTracked: "den-denon",
		},
		{
			name:        "the lift fails",
			liftFails:   true,
			wantApplies: "living-room-denon: GAME, living-room-denon: lift",
			wantTracked: "living-room-denon",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := receiverCluster()
			cluster.receivers["den-denon"] = &Receiver{
				Metadata: ObjectMeta{Name: "den-denon"},
				Spec:     ReceiverSpec{Inputs: []ReceiverInput{{Name: "MPLAY", Machine: "nuc6", Monitor: testMonitor}}},
			}
			media := testOperator(t, cluster, make(chan struct{}, 1))

			runPlayers(media, []Player{*housePlayer()}, standingPlays())

			cluster.receivers["living-room-denon"].Spec.Inputs[1].Machine = "nuc6"
			cluster.receivers["den-denon"].Spec.Inputs[0].Machine = testNode
			cluster.sessionsFail = each.liftFails
			runPlayers(media, []Player{*housePlayer()}, standingPlays())

			mustMatch(t, appliedSessions(cluster), each.wantApplies)
			mustMatch(t, media.receiverSessions[playerKey("house", "theater")].receiver, each.wantTracked)
		})
	}
}

// appliedSessions reads every apply the pass made as one line: the
// Receiver it named, and the input the session selected or the lift that
// carried no session at all.
func appliedSessions(cluster *fakeCluster) string {
	applies := make([]string, len(cluster.sessions))
	for index, applied := range cluster.sessions {
		applies[index] = applied.name + ": lift"
		if applied.session != nil {
			applies[index] = applied.name + ": " + applied.session.Input
		}
	}
	return strings.Join(applies, ", ")
}
