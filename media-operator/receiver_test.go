package main

// These tests cover media-operator's side of the Receiver resource:
// matching a Player's screen to an input, the session applied for a
// matched Player, the active flag that follows a running Play, and
// removing the session.

import (
	"strconv"
	"strings"
	"testing"
)

// houseReceiver is the Receiver that the house Player's cable goes to.
// Its first input belongs to another machine, so a match shows that the
// code compares both the machine and the monitor id.
func houseReceiver() *Receiver {
	return &Receiver{
		Metadata: ObjectMeta{Name: "den-receiver"},
		Spec: ReceiverSpec{Inputs: []ReceiverInput{
			{Name: "CBL/SAT", Machine: "node-6", Monitor: testMonitor},
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

// receiverCluster is a cluster whose theater Player draws on a screen
// that is connected to one Receiver input.
func receiverCluster() *fakeCluster {
	cluster := screenCluster()
	cluster.receivers["den-receiver"] = houseReceiver()
	return cluster
}

// An input matches only when the machine and the monitor id both
// match, because a receiver forwards the same EDID on every input.
func TestAnInputMatchesTheMachineAndTheMonitorTogether(t *testing.T) {
	cases := []struct {
		name    string
		node    string
		monitor string
		want    string
	}{
		{name: "both values name the input", node: testNode, monitor: testMonitor, want: "GAME"},
		{name: "another machine on the same monitor", node: "node-7", monitor: testMonitor},
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

// An entry with no input name is not an input, so a half-written
// Receiver matches nothing.
func TestAnInputWithNoNameMatchesNothing(t *testing.T) {
	receiver := houseReceiver()
	receiver.Spec.Inputs = []ReceiverInput{{Machine: testNode, Monitor: testMonitor}}

	_, matched := matchReceiverInput(receiver, testNode, testMonitor)

	mustMatch(t, matched, false)
}

// The lookup lists the Receivers from the view once, so later matches
// in the same pass send the API server no request.
func TestTheReceiversComeFromTheView(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	lookup := media.receiverLookup()

	receiver, input, matched := lookup.matchFor(testNode, testMonitor)

	mustMatch(t, matched, true)
	mustMatch(t, receiver.Metadata.Name, "den-receiver")
	mustMatch(t, input, "GAME")

	_, _, matched = lookup.matchFor(testNode, testMonitor)

	mustMatch(t, matched, true)
	mustMatchAll(t, cluster.requests, nil)
}

// When the pass could not resolve a Player's screen, no Receiver
// matches, and the lookup does not list the Receivers.
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

// A cluster that runs no equipment-operator does not serve the
// Receiver collection, and a failed list gives the same result: no
// match, and the Player plays without equipment.
func TestAClusterWithNoReceiversMatchesNothing(t *testing.T) {
	cases := []struct {
		name  string
		shape func(*fakeCluster)
	}{
		{name: "the collection is absent", shape: func(c *fakeCluster) { c.receiversAbsent = true }},
		{name: "the read fails", shape: func(c *fakeCluster) { c.fails[receiversPath] = true }},
		{name: "the cluster has no receiver", shape: func(c *fakeCluster) { c.receivers = map[string]*Receiver{} }},
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

// A failed list of the Receivers does not show that the wiring
// changed, so the Player keeps its entry in media.playerReceivers, and a button
// press or a Television's request for its screen can still reach the
// Receiver.
func TestAFailedReceiversListKeepsThePlayerWired(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	media.idleDisplayClass = "display-draw"
	runPlayers(media, []Player{*housePlayer()}, nil)

	cluster.fails[receiversPath] = true
	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, wiredReceiver(media), "den-receiver")
	entry, wired := media.playerReceivers.receiverFor(playerKey("house", "theater"))
	mustMatch(t, wired, true)
	mustMatch(t, entry.name, "den-receiver")
}

// The Player's reachable value is the status of the Receiver's
// Reachable condition, or empty when the Receiver has no such
// condition.
func TestTheReachableValueIsTheConditionStatus(t *testing.T) {
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

// A Player has an unfinished Play while a Play in its namespace names it
// and has not finished. The playback pod exists under the same
// condition.
func TestAPlayThatHasNotFinishedCountsAsUnfinished(t *testing.T) {
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
		{name: "no play names the player", want: false},
		{name: "a play with no status yet", phase: "", want: true},
		{name: "a pending play", phase: phasePending, want: true},
		{name: "a running play", phase: phaseRunning, want: true},
		{name: "a failed play keeps its pod", phase: phaseFailed, want: true},
		{name: "a finished play is over", phase: phaseFinished, want: false},
		{name: "another player's play", plays: []Play{*otherPlayer}, want: false},
		{name: "another namespace's play", plays: []Play{*otherNamespace}, want: false},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			plays := each.plays
			if plays == nil && each.name != "no play names the player" {
				play := housePlay("https://nas/film.mkv")
				play.Status.Phase = each.phase
				plays = []Play{*play}
			}

			mustMatch(t, playerHasUnfinishedPlay(housePlayer(), plays), each.want)
		})
	}
}

// The Player status names the Receiver its cable goes to and copies
// the Receiver's Reachable condition.
//
// An idle Player also has a session, with the active flag false, so
// equipment-operator controls the volume while nothing plays and a
// volume press at the idle screen changes the room's volume.
func TestAnIdlePlayerReportsItsReceiverAndAppliesAnInactiveSession(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, nil)

	status := cluster.players["theater"].Status.Receiver
	if status == nil {
		t.Fatal("status.receiver is nil, want the Receiver den-receiver")
	}
	mustMatch(t, status.Name, "den-receiver")
	mustMatch(t, status.Input, "GAME")
	mustMatch(t, status.Reachable, "True")
	mustMatch(t, len(cluster.sessions), 1)
	mustMatch(t, *cluster.sessions[0].session, ReceiverSession{
		Player: "house/theater",
		Input:  "GAME",
		Awake:  true,
	})
}

// A Player connected straight to its panel has no status.receiver.
func TestAPlayerWithNoReceiverReportsNoReceiverStatus(t *testing.T) {
	cluster := screenCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, nil)

	if status := cluster.players["theater"].Status.Receiver; status != nil {
		t.Errorf("status.receiver is %+v, want nil", status)
	}
}

// Every Player's status.idle.bus has the power topic, whether or not
// the Player is connected through a Receiver, because spec.power
// applies to every Player's screen.
func TestTheBusAlwaysCarriesThePowerTopic(t *testing.T) {
	cases := []struct {
		name    string
		cluster func() *fakeCluster
		want    string
	}{
		{name: "a player connected through a receiver", cluster: receiverCluster, want: "den-receiver"},
		{name: "a player connected straight to a panel", cluster: screenCluster, want: "none"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := each.cluster()
			media := testOperator(t, cluster, make(chan struct{}, 1))
			media.idleDisplayClass = "display-draw"

			runPlayers(media, []Player{*housePlayer()}, nil)

			mustMatch(t, cluster.players["theater"].Status.Idle.Bus.PowerTopic,
				playerPowerTopic(defaultTopicBase, "house", "theater"))
			mustMatch(t, wiredReceiver(media), each.want)
		})
	}
}

// wiredReceiver returns the name of the Receiver in media.playerReceivers for
// the house Player, or "none" when it has no entry.
func wiredReceiver(media *operator) string {
	entry, wired := media.playerReceivers.receiverFor(playerKey("house", "theater"))
	if !wired {
		return "none"
	}
	return entry.name
}

// runPlayers runs one pass over the Players. It clears the screen and
// receiver caches first, as a real pass does, so changes made to the
// cluster between passes are read.
func runPlayers(media *operator, players []Player, plays []Play) {
	media.screenCache, media.receiverCache = nil, nil
	media.reconcilePlayers(players, plays, "", nil)
}

// runningPlays returns one running Play for the house Player.
func runningPlays() []Play {
	play := housePlay("https://nas/film.mkv")
	play.Status.Phase = phaseRunning
	return []Play{*play}
}

// While a Play is running, the operator applies the session under its
// own field manager, and applies it once, because equipment-operator
// acts once on each change of power or input.
func TestARunningPlayAppliesTheSessionOnce(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, runningPlays())
	runPlayers(media, []Player{*housePlayer()}, runningPlays())

	mustMatch(t, len(cluster.sessions), 1)
	mustMatch(t, cluster.sessions[0].name, "den-receiver")
	mustMatch(t, cluster.sessions[0].manager, applyFieldManager)
	mustMatch(t, *cluster.sessions[0].session, ReceiverSession{
		Player: "house/theater",
		Input:  "GAME",
		Active: true,
		Awake:  true,
	})
	mustMatch(t, media.receiverSessions[playerKey("house", "theater")].receiver, "den-receiver")
}

// When a Play starts, the active flag becomes true, and when it ends
// the flag becomes false. Neither change removes the session, so the
// room keeps its input and its volume control across the film.
func TestAPlayStartAndEndChangeOnlyTheActiveFlag(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, nil)
	runPlayers(media, []Player{*housePlayer()}, runningPlays())
	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, appliedActive(cluster), "false, true, false")
	mustMatch(t, media.receiverSessions[playerKey("house", "theater")].receiver, "den-receiver")
	if cluster.receivers["den-receiver"].Status.Session == nil {
		t.Error("status.session of den-receiver is nil, want the session")
	}
}

// appliedActive returns the active flag of every session apply the
// passes made, in order, with "lift" for each removal.
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

// sendPanelDesire delivers one panel desire to the operator as a bus
// message, in the format the idle client publishes.
func sendPanelDesire(media *operator, desire string) {
	media.handleBusMessage(playerPanelTopic(defaultTopicBase, "house", "theater"),
		[]byte(`{"desire":"`+desire+`"}`))
}

// The awake flag follows the Player's panel desire. A Player with no
// published desire is awake.
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
				sendPanelDesire(media, each.desire)
			}

			runPlayers(media, []Player{*housePlayer()}, nil)

			mustMatch(t, len(cluster.sessions), 1)
			mustMatch(t, *cluster.sessions[0].session, ReceiverSession{
				Player: "house/theater",
				Input:  "GAME",
				Awake:  each.want,
			})
		})
	}
}

// earlierSession is a session that an earlier operator process left
// on the house Receiver.
func earlierSession(awake bool) *ReceiverSession {
	return &ReceiverSession{
		Player: "house/theater",
		Input:  "GAME",
		Awake:  awake,
	}
}

// sessionField puts a session in status.session, where media-operator
// writes it, or in spec.session, where an earlier version wrote it.
type sessionField func(receiver *Receiver, session *ReceiverSession)

func inStatus(receiver *Receiver, session *ReceiverSession) { receiver.Status.Session = session }
func inSpec(receiver *Receiver, session *ReceiverSession)   { receiver.Spec.Session = session }

// An operator that restarts finds the session that its earlier process
// left on the Receiver. The pass sends nothing, both before the bus delivers
// the panel desire and after a desire that matches the session. So a
// slow bus never powers the equipment on in a dark room.
//
// A session that only exists in spec.session, written by an earlier
// version, moves to status.session once with the same flags, and
// spec.session is released. equipment-operator treats that move as the
// same session and sends nothing.
func TestARestartWithTheSessionOnTheReceiverAppliesNothing(t *testing.T) {
	cases := []struct {
		name     string
		put      sessionField
		awake    bool
		desire   string
		moved    int
		released int
	}{
		{name: "a dark room before the desire arrives", put: inStatus, awake: false},
		{name: "a lit room before the desire arrives", put: inStatus, awake: true},
		{name: "a dark room with the off desire", put: inStatus, awake: false, desire: panelDesireOff},
		{name: "a lit room with the on desire", put: inStatus, awake: true, desire: panelDesireOn},
		{name: "a dark room with the session in spec", put: inSpec, awake: false, moved: 1, released: 1},
		{name: "a lit room with the session in spec and the on desire", put: inSpec, awake: true, desire: panelDesireOn, moved: 1, released: 1},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := receiverCluster()
			each.put(cluster.receivers["den-receiver"], earlierSession(each.awake))
			media := testOperator(t, cluster, make(chan struct{}, 1))
			if each.desire != "" {
				sendPanelDesire(media, each.desire)
			}

			runPlayers(media, []Player{*housePlayer()}, nil)
			runPlayers(media, []Player{*housePlayer()}, nil)

			mustMatch(t, len(cluster.sessions), each.moved)
			mustMatch(t, len(cluster.specReleases), each.released)
			if each.moved > 0 {
				mustMatch(t, *cluster.sessions[0].session, *earlierSession(each.awake))
			}
		})
	}
}

// A session that an older version applied has the volume and power
// topics. The pass applies it again without them, so equipment-operator
// stops reading the bus for the Player, and the next pass sends
// nothing.
func TestASessionWithTopicsIsAppliedAgainWithoutThem(t *testing.T) {
	cluster := receiverCluster()
	session := earlierSession(true)
	session.VolumeTopic = playerVolumeTopic(defaultTopicBase, "house", "theater")
	session.PowerTopic = playerPowerTopic(defaultTopicBase, "house", "theater")
	cluster.receivers["den-receiver"].Status.Session = session
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, nil)
	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, len(cluster.sessions), 1)
	mustMatch(t, *cluster.sessions[0].session, *earlierSession(true))
}

// The session is written to the status subresource under this
// operator's field manager, and the spec stays as the cluster owner
// wrote it.
func TestTheSessionIsWrittenToTheStatus(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, runningPlays())

	mustMatch(t, len(cluster.sessions), 1)
	mustMatch(t, cluster.sessions[0].manager, applyFieldManager)
	mustMatch(t, countPathRequests(cluster.requests, "PATCH "+receiversPath+"/den-receiver/status"), 1)
	mustMatch(t, cluster.receivers["den-receiver"].Status.Session.Active, true)
	mustMatch(t, cluster.receivers["den-receiver"].Spec.Session == nil, true)
	mustMatch(t, len(cluster.specReleases), 0)
}

// A spec.session that an earlier version wrote is released once
// status.session has the session. That is after a status write, or
// immediately when the status already has the same session. The release
// uses the field manager that wrote the spec, and the passes send it
// once.
func TestTheOldSpecSessionIsReleasedOnceTheStatusHasTheSession(t *testing.T) {
	cases := []struct {
		name         string
		spec         *ReceiverSession
		status       *ReceiverSession
		wantSessions int
	}{
		{name: "a spec session that differs", spec: playingSession(), wantSessions: 1},
		{name: "the same session in spec and status", spec: earlierSession(true), status: earlierSession(true)},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := receiverCluster()
			receiver := cluster.receivers["den-receiver"]
			receiver.Spec.Session, receiver.Status.Session = each.spec, each.status
			media := testOperator(t, cluster, make(chan struct{}, 1))

			runPlayers(media, []Player{*housePlayer()}, nil)
			runPlayers(media, []Player{*housePlayer()}, nil)

			mustMatch(t, len(cluster.sessions), each.wantSessions)
			mustMatch(t, appliedReleases(cluster), "den-receiver: "+applyFieldManager)
			mustMatch(t, receiver.Spec.Session == nil, true)
		})
	}
}

// When a Player is deleted after its spec.session moved to the status,
// the operator removes both. Then equipment-operator, which uses
// spec.session when status.session is empty, finds no session there
// either.
func TestRemovingASessionReleasesTheOldSpecSessionToo(t *testing.T) {
	cluster := receiverCluster()
	cluster.receivers["den-receiver"].Spec.Session = earlierSession(true)
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, nil)
	runPlayers(media, nil, nil)

	mustMatch(t, appliedReleases(cluster), "den-receiver: "+applyFieldManager)
	mustMatch(t, appliedSessions(cluster), "den-receiver: GAME, den-receiver: lift")
	mustMatch(t, cluster.receivers["den-receiver"].Spec.Session == nil, true)
}

// playingSession is a session left from a Play that has since ended,
// so the session this pass applies differs from it only in the active
// flag.
func playingSession() *ReceiverSession {
	session := earlierSession(true)
	session.Active = true
	return session
}

// appliedReleases returns every spec release the passes made as one
// line: the Receiver and the field manager of each.
func appliedReleases(cluster *fakeCluster) string {
	releases := make([]string, len(cluster.specReleases))
	for index, applied := range cluster.specReleases {
		releases[index] = applied.name + ": " + applied.manager
	}
	return strings.Join(releases, ", ")
}

// A desire that differs from the session on the Receiver comes from a
// person's button press or a timer, and it is applied to the
// equipment.
func TestADesireThatDiffersFromTheSessionOnTheReceiverIsApplied(t *testing.T) {
	cluster := receiverCluster()
	cluster.receivers["den-receiver"].Status.Session = earlierSession(false)
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, nil)
	sendPanelDesire(media, panelDesireOn)
	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, appliedAwake(cluster), "true")
}

// The press that turns the panel on is applied on the next pass,
// because the comparison covers the whole session, and the awake flag
// changed.
func TestThePressThatWakesThePanelAppliesTheSessionAgain(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	sendPanelDesire(media, panelDesireOff)
	runPlayers(media, []Player{*housePlayer()}, nil)
	sendPanelDesire(media, panelDesireOn)
	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, appliedAwake(cluster), "false, true")
}

// appliedAwake returns the awake flag of every session apply the passes
// made, in order, with "lift" for each removal.
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

// Deleting a Player removes its session from the Receiver.
func TestADeletedPlayerLosesItsSession(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, nil)
	runPlayers(media, nil, nil)

	mustMatch(t, len(media.receiverSessions), 0)
	if cluster.receivers["den-receiver"].Status.Session != nil {
		t.Error("status.session of den-receiver is set, want nil")
	}
}

// When the API server rejects the removal, the entry stays, so the
// next pass writes to the Receiver again.
func TestAFailedSessionRemovalRetriesOnTheNextPass(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	key := playerKey("house", "theater")

	runPlayers(media, []Player{*housePlayer()}, runningPlays())
	cluster.sessionsFail = true
	runPlayers(media, nil, nil)

	mustMatch(t, media.receiverSessions[key].receiver, "den-receiver")

	cluster.sessionsFail = false
	runPlayers(media, nil, nil)

	mustMatch(t, len(media.receiverSessions), 0)
	if cluster.receivers["den-receiver"].Status.Session != nil {
		t.Error("status.session of den-receiver is set, want nil")
	}
}

// When the Receiver no longer exists, the removal returns not found,
// which counts as done, so the entry is dropped.
func TestRemovingASessionFromADeletedReceiverDropsTheEntry(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, runningPlays())
	delete(cluster.receivers, "den-receiver")
	runPlayers(media, []Player{*housePlayer()}, nil)

	mustMatch(t, len(media.receiverSessions), 0)
}

// When the API server rejects an apply, the operator records nothing,
// so the next pass sends the session again.
func TestAFailedSessionApplyIsSentAgain(t *testing.T) {
	cluster := receiverCluster()
	cluster.sessionsFail = true
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, runningPlays())

	mustMatch(t, len(media.receiverSessions), 0)

	cluster.sessionsFail = false
	runPlayers(media, []Player{*housePlayer()}, runningPlays())

	mustMatch(t, len(cluster.sessions), 2)
	mustMatch(t, media.receiverSessions[playerKey("house", "theater")].receiver, "den-receiver")
}

// A session that changed is applied again, because the session's input
// is the input that the equipment selects.
func TestASessionThatChangedIsAppliedAgain(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	runPlayers(media, []Player{*housePlayer()}, runningPlays())
	cluster.receivers["den-receiver"].Spec.Inputs[1].Name = "MPLAY"
	runPlayers(media, []Player{*housePlayer()}, runningPlays())

	mustMatch(t, len(cluster.sessions), 2)
	mustMatch(t, cluster.sessions[1].session.Input, "MPLAY")
}

// The pass that creates the pod applies the session first, so the
// equipment is on and set to the right input by the time mpv draws its
// first frame.
func TestTheSessionReachesTheReceiverBeforeThePodIsCreated(t *testing.T) {
	cluster := receiverCluster()
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	applied := indexOfRequest(cluster.requests, "PATCH "+receiversPath+"/den-receiver/status")
	created := indexOfRequest(cluster.requests, "POST /api/v1/namespaces/house/pods")
	if applied < 0 || created < 0 {
		t.Fatalf("the requests are %v, want a Receiver status patch and a pod creation", cluster.requests)
	}
	if applied > created {
		t.Errorf("the requests are %v, want the session patch before the pod creation", cluster.requests)
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

// indexOfRequest returns the position of a request in the list of
// requests the pass made, or -1 when the pass did not make it.
func indexOfRequest(requests []string, want string) int {
	for index, request := range requests {
		if request == want {
			return index
		}
	}
	return -1
}

// When a Player's screen moves to another Receiver, the operator
// removes the session from the old Receiver before it applies the
// session to the new one. If the removal fails, the new Receiver gets
// nothing until the next pass.
func TestAMovedPlayerLosesTheOldSessionBeforeTheNewOneIsApplied(t *testing.T) {
	cases := []struct {
		name         string
		removalFails bool
		wantApplies  string
		wantTracked  string
	}{
		{
			name:        "the removal succeeds",
			wantApplies: "den-receiver: GAME, den-receiver: lift, den-denon: MPLAY",
			wantTracked: "den-denon",
		},
		{
			name:         "the removal fails",
			removalFails: true,
			wantApplies:  "den-receiver: GAME, den-receiver: lift",
			wantTracked:  "den-receiver",
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := receiverCluster()
			cluster.receivers["den-denon"] = &Receiver{
				Metadata: ObjectMeta{Name: "den-denon"},
				Spec:     ReceiverSpec{Inputs: []ReceiverInput{{Name: "MPLAY", Machine: "node-6", Monitor: testMonitor}}},
			}
			media := testOperator(t, cluster, make(chan struct{}, 1))

			runPlayers(media, []Player{*housePlayer()}, runningPlays())

			cluster.receivers["den-receiver"].Spec.Inputs[1].Machine = "node-6"
			cluster.receivers["den-denon"].Spec.Inputs[0].Machine = testNode
			cluster.sessionsFail = each.removalFails
			runPlayers(media, []Player{*housePlayer()}, runningPlays())

			mustMatch(t, appliedSessions(cluster), each.wantApplies)
			mustMatch(t, media.receiverSessions[playerKey("house", "theater")].receiver, each.wantTracked)
		})
	}
}

// appliedSessions returns every apply the pass made as one line. Each
// entry is the Receiver name and either the session's input or "lift"
// for an apply with no session.
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
