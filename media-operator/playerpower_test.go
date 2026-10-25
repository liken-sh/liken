package main

// These tests cover what a pass does when a Player's spec.power differs
// from the value its status records: Off powers the receiver, the TV
// behind it, and the panel down, lowers the screen's shade, and stops a
// standing Play; On wakes the screen and lifts the panel's override. The
// status records the value the pass applied, so a restarted operator
// acts on a change once and adopts a Player whose status records none.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// powerPass runs one pass over the cluster's own copy of the house
// theater, the way a pass reads it from the store.
func powerPass(media *operator, cluster *fakeCluster, plays []Play) {
	runPlayers(media, []Player{*cluster.players["theater"]}, plays)
}

// settledAt is a cluster whose theater recorded the given power in an
// earlier pass, with every write that pass made dropped from the record.
func settledAt(t *testing.T, cluster *fakeCluster, media *operator, power string) {
	t.Helper()
	cluster.players["theater"].Spec.Power = power
	powerPass(media, cluster, nil)
	mustMatch(t, cluster.players["theater"].Status.Power, specPowerOf(power))
	cluster.sessions, cluster.applies = nil, nil
}

// specPowerOf reads an absent spec.power the way the operator reads it.
func specPowerOf(power string) string {
	if power == "" {
		return playerPowerOn
	}
	return power
}

// publishedAsks drains every publish that reached the broker once the
// bubble has settled, and answers each one that is not a unit's status,
// as topic and payload.
func publishedAsks(t *testing.T, broker *fakeBroker) []string {
	t.Helper()
	synctest.Wait()
	var asks []string
	for {
		select {
		case published := <-broker.pubs:
			if strings.HasSuffix(published.topic, "/status") {
				continue
			}
			asks = append(asks, published.topic+" "+string(published.payload))
		default:
			return asks
		}
	}
}

// powerApplied answers the PowerApplied condition the theater's status
// carries.
func powerApplied(t *testing.T, cluster *fakeCluster) PlayerCondition {
	t.Helper()
	condition, held := heldCondition(cluster.players["theater"].Status.Conditions, powerAppliedCondition)
	if !held {
		t.Fatalf("the Player carries no PowerApplied condition: %+v", cluster.players["theater"].Status.Conditions)
	}
	return condition
}

// powerEvents answers the Events on the theater that are not the
// Screen condition's.
func powerEvents(cluster *fakeCluster) []string {
	var posted []string
	for _, event := range postedOn(cluster, "Player", "theater") {
		if strings.Contains(event, " Present: ") {
			continue
		}
		posted = append(posted, event)
	}
	return posted
}

// A Player whose status records no power adopts its spec and asks the
// hardware for nothing, the way the operator adopts the sessions it
// finds at start. Off still holds the panel down and the session
// asleep, because those follow spec.power on every pass.
func TestAPlayerWithNoRecordedPowerAdoptsItsSpec(t *testing.T) {
	cases := []struct {
		name  string
		power string
		want  string
	}{
		{name: "absent", want: playerPowerOn},
		{name: "On", power: playerPowerOn, want: playerPowerOn},
		{name: "Off", power: playerPowerOff, want: playerPowerOff},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := receiverCluster()
				media, broker := caughtUpOperator(t, cluster)
				cluster.players["theater"].Spec.Power = each.power

				powerPass(media, cluster, nil)

				mustMatch(t, cluster.players["theater"].Status.Power, each.want)
				mustMatchAll(t, publishedAsks(t, broker), nil)
				mustMatch(t, cluster.sessions[0].session.PowerAsk == nil, true)
				_, held := heldCondition(cluster.players["theater"].Status.Conditions, powerAppliedCondition)
				mustMatch(t, held, false)
			})
		})
	}
}

// Off asks the receiver for off before the session's awake flag turns
// false, so the equipment operator reads the standby first. It holds
// the panel down with the power override, lowers the screen's shade,
// and stops the standing Play the way the remote's stop does.
func TestTurningAPlayerOffPowersEverythingDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		media, broker := caughtUpOperator(t, cluster)
		settledAt(t, cluster, media, playerPowerOn)
		publishedAsks(t, broker)
		cluster.players["theater"].Spec.Power = playerPowerOff

		powerPass(media, cluster, standingPlays())

		mustMatch(t, cluster.sessions[0].session.PowerAsk.Action, receiverPowerOff)
		mustMatch(t, cluster.sessions[0].session.Awake, true)
		mustMatch(t, cluster.sessions[len(cluster.sessions)-1].session.Awake, false)
		mustMatch(t, *cluster.displays[testMonitor].Spec.Override, DisplayOverride{Power: displayPowerOff})
		mustMatchAll(t, publishedAsks(t, broker), []string{
			"liken/media/players/house/theater/power " + `{"action":"sleep"}`,
			"liken/media/plays/house/movie/commands " + `{"action":"stop"}`,
		})
		mustMatch(t, cluster.players["theater"].Status.Power, playerPowerOff)
		applied := powerApplied(t, cluster)
		mustMatch(t, applied.Status, conditionTrue)
		mustMatch(t, applied.Reason, powerReasonApplied)
		mustMatch(t, applied.Message, "Off: applied power Off to display DP-1, asked receiver den-receiver for off, "+
			"asked the screen to sleep, asked play movie to stop")
		mustMatchAll(t, powerEvents(cluster), []string{"Normal Applied: " + applied.Message})
	})
}

// On wakes the screen, which states the on desire, and the session's
// awake flag that follows the desire wakes the TV and the receiver. The
// pass lifts the power override at once, before the client answers.
func TestTurningAPlayerOnWakesTheScreenAndLiftsTheOverride(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		media, broker := caughtUpOperator(t, cluster)
		settledAt(t, cluster, media, playerPowerOff)
		publishedAsks(t, broker)
		cluster.players["theater"].Spec.Power = playerPowerOn

		powerPass(media, cluster, nil)

		mustMatch(t, cluster.displays[testMonitor].Spec.Override == nil, true)
		mustMatchAll(t, publishedAsks(t, broker), []string{
			"liken/media/players/house/theater/power " + `{"action":"wake"}`,
		})
		mustMatch(t, cluster.players["theater"].Status.Power, playerPowerOn)
		mustMatch(t, powerApplied(t, cluster).Message,
			"On: lifted the override on display DP-1, asked the screen to wake")

		statePanelDesire(media, panelDesireOn)
		powerPass(media, cluster, nil)

		mustMatch(t, cluster.sessions[len(cluster.sessions)-1].session.Awake, true)
	})
}

// Each change of spec.power posts one Event, though the condition stays
// True with the same reason and only its message moves. A pass that
// changes nothing posts nothing.
func TestEachPowerChangePostsOneEvent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		media, _ := caughtUpOperator(t, cluster)
		settledAt(t, cluster, media, playerPowerOn)

		for _, power := range []string{playerPowerOff, playerPowerOn} {
			cluster.players["theater"].Spec.Power = power
			powerPass(media, cluster, nil)
		}
		powerPass(media, cluster, nil)

		mustMatchAll(t, powerEvents(cluster), []string{
			"Normal Applied: Off: applied power Off to display DP-1, asked receiver den-receiver for off, " +
				"asked the screen to sleep",
			"Normal Applied: On: lifted the override on display DP-1, asked the screen to wake",
		})
	})
}

// A Player with no Receiver is one monitor: Off powers its panel down
// and lowers the shade, and On lifts the override and wakes the screen.
func TestAPlayerWithNoReceiverTurnsItsPanel(t *testing.T) {
	cases := []struct {
		power    string
		override string
		ask      string
		message  string
	}{
		{
			power:    playerPowerOff,
			override: "power Off",
			ask:      `{"action":"sleep"}`,
			message:  "Off: applied power Off to display DP-1, asked the screen to sleep",
		},
		{
			power:    playerPowerOn,
			override: "no override",
			ask:      `{"action":"wake"}`,
			message:  "On: lifted the override on display DP-1, asked the screen to wake",
		},
	}
	for _, each := range cases {
		t.Run(each.power, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := screenCluster()
				media, broker := caughtUpOperator(t, cluster)
				from := playerPowerOn
				if each.power == playerPowerOn {
					from = playerPowerOff
				}
				settledAt(t, cluster, media, from)
				publishedAsks(t, broker)
				cluster.players["theater"].Spec.Power = each.power

				powerPass(media, cluster, nil)

				mustMatch(t, describeOverride(cluster.displays[testMonitor].Spec.Override), each.override)
				mustMatchAll(t, publishedAsks(t, broker), []string{
					"liken/media/players/house/theater/power " + each.ask,
				})
				mustMatch(t, powerApplied(t, cluster).Message, each.message)
			})
		})
	}
}

// A write that fails leaves the recorded power where it was, so the
// next pass asks again, and the condition says which write failed. The
// pass after the write lands records the power and posts the recovery.
// A Receiver that refuses the ask refuses the session's awake flag too,
// and that write posts its own failure and recovery.
func TestAFailedPowerWriteIsAskedAgain(t *testing.T) {
	cases := []struct {
		name   string
		fail   func(*fakeCluster, bool)
		reason string
		events []string
	}{
		{
			name:   "the receiver",
			reason: powerReasonReceiverAskFailed,
			fail:   func(cluster *fakeCluster, fails bool) { cluster.sessionsFail = fails },
			events: []string{
				"Warning " + reasonReceiverWriteFailed, "Warning " + powerReasonReceiverAskFailed,
				"Normal " + reasonReceiverWriteRecovered, "Normal " + powerReasonApplied,
			},
		},
		{
			name:   "the display",
			reason: powerReasonDisplayWriteFailed,
			fail:   func(cluster *fakeCluster, fails bool) { cluster.applyFails = fails },
			events: []string{"Warning " + powerReasonDisplayWriteFailed, "Normal " + powerReasonApplied},
		},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := receiverCluster()
				media, _ := caughtUpOperator(t, cluster)
				settledAt(t, cluster, media, playerPowerOn)
				cluster.players["theater"].Spec.Power = playerPowerOff
				each.fail(cluster, true)

				powerPass(media, cluster, nil)

				mustMatch(t, cluster.players["theater"].Status.Power, playerPowerOn)
				failed := powerApplied(t, cluster)
				mustMatch(t, failed.Status, conditionFalse)
				mustMatch(t, failed.Reason, each.reason)

				each.fail(cluster, false)
				powerPass(media, cluster, nil)

				mustMatch(t, cluster.players["theater"].Status.Power, playerPowerOff)
				mustMatch(t, powerApplied(t, cluster).Status, conditionTrue)
				mustMatchAll(t, typesOf(powerEvents(cluster)), each.events)
			})
		})
	}
}

// typesOf cuts each Event to its type and its reason, because a failed
// write's message carries the fake server's own words.
func typesOf(posted []string) []string {
	cut := make([]string, len(posted))
	for index, event := range posted {
		cut[index], _, _ = strings.Cut(event, ":")
	}
	return cut
}

// The pass after a change asks nothing again, because the status
// records the value it applied.
func TestARecordedPowerIsNotAskedAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		media, broker := caughtUpOperator(t, cluster)
		settledAt(t, cluster, media, playerPowerOn)
		cluster.players["theater"].Spec.Power = playerPowerOff
		powerPass(media, cluster, nil)
		publishedAsks(t, broker)
		cluster.sessions, cluster.applies = nil, nil

		powerPass(media, cluster, nil)

		mustMatchAll(t, publishedAsks(t, broker), nil)
		mustMatch(t, len(cluster.sessions), 0)
		mustMatch(t, len(cluster.applies), 0)
	})
}

// A Player that is off holds its session asleep whatever the screen
// client states, so a desire left on by a client under a film does not
// wake the equipment.
func TestAPlayerThatIsOffHoldsTheSessionAsleep(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	cluster.players["theater"].Spec.Power = playerPowerOff
	statePanelDesire(media, panelDesireOn)

	powerPass(media, cluster, nil)

	mustMatch(t, appliedAwake(cluster), "false")
}

// The status records the applied power beside the rest, and the
// PowerApplied condition follows the Screen condition.
func TestThePowerAppliedConditionFollowsTheScreenCondition(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		media, _ := caughtUpOperator(t, cluster)
		settledAt(t, cluster, media, playerPowerOn)
		cluster.players["theater"].Spec.Power = playerPowerOff

		powerPass(media, cluster, nil)

		var types []string
		for _, condition := range cluster.players["theater"].Status.Conditions {
			types = append(types, condition.Type)
		}
		mustMatchAll(t, types, []string{screenConditionType, powerAppliedCondition})
		encoded, err := json.Marshal(cluster.players["theater"].Status)
		mustSucceed(t, err)
		mustMatch(t, strings.Contains(string(encoded), `"power":"Off"`), true)
	})
}

// A failed change that spec.power takes back before it lands still
// asks the hardware, because some of its writes may have landed: the
// receiver may be off already. The pass asks for the value spec.power
// wants now.
func TestAFailedChangeTakenBackAsksForTheValueNow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		media, broker := caughtUpOperator(t, cluster)
		settledAt(t, cluster, media, playerPowerOn)
		cluster.players["theater"].Spec.Power = playerPowerOff
		cluster.applyFails = true
		powerPass(media, cluster, nil)
		publishedAsks(t, broker)
		cluster.applyFails = false
		cluster.players["theater"].Spec.Power = playerPowerOn

		powerPass(media, cluster, nil)

		mustMatchAll(t, publishedAsks(t, broker), []string{
			"liken/media/players/house/theater/power " + `{"action":"wake"}`,
		})
		mustMatch(t, cluster.players["theater"].Status.Power, playerPowerOn)
		mustMatch(t, powerApplied(t, cluster).Status, conditionTrue)
	})
}

// offCluster is a receiver cluster whose theater is off and has
// recorded it, with a new Play on it.
func offCluster(power string) *fakeCluster {
	cluster := receiverCluster()
	cluster.players["theater"].Spec.Power = power
	cluster.players["theater"].Status.Power = specPowerOf(power)
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	return cluster
}

// A Play that starts turns its Player on first, with the same apply a
// key press sends, so the session it wakes is a Player that is on.
func TestAPlayThatStartsTurnsItsPlayerOn(t *testing.T) {
	cases := []struct {
		name    string
		power   string
		applies []playerApplied
	}{
		{name: "off", power: playerPowerOff, applies: []playerApplied{
			{fields: `{"power":"On"}`, manager: powerFieldManager, force: true},
		}},
		{name: "on", power: playerPowerOn},
		{name: "absent"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := offCluster(each.power)
			media := testOperator(t, cluster, make(chan struct{}, 1))

			media.pass()

			mustMatchAll(t, cluster.playerApplies, each.applies)
			mustMatch(t, specPower(cluster.players["theater"]), playerPowerOn)
			mustMatch(t, cluster.pods["movie-playback"] != nil, true)
			mustMatch(t, cluster.sessions[0].session.Awake, true)
		})
	}
}

// The pass after the start runs the change from Off to On the way a key
// press does: it wakes the screen, records the power, and posts one
// Event.
func TestAPlayThatStartsRunsTheChangeToOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := offCluster(playerPowerOff)
		media, broker := caughtUpOperator(t, cluster)

		media.pass()
		media.pass()

		mustMatch(t, slices.Contains(publishedAsks(t, broker),
			"liken/media/players/house/theater/power "+`{"action":"wake"}`), true)
		mustMatch(t, cluster.players["theater"].Status.Power, playerPowerOn)
		mustMatch(t, powerApplied(t, cluster).Reason, powerReasonApplied)
		mustMatch(t, len(powerEvents(cluster)), 1)
	})
}

// A refused apply leaves the Player off, and the run still starts. The
// session it wakes stays asleep, because the Player is still off.
func TestARefusedApplyStillStartsThePlay(t *testing.T) {
	cluster := offCluster(playerPowerOff)
	cluster.playerApplyFails = true
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	mustMatch(t, cluster.players["theater"].Spec.Power, playerPowerOff)
	mustMatch(t, cluster.pods["movie-playback"] != nil, true)
	mustMatch(t, appliedAwake(cluster), "false")
}
