package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// powerPass runs one pass over the theater Player as the fake cluster
// stores it.
func powerPass(media *operator, cluster *fakeCluster, plays []Play) {
	runPlayers(media, []Player{*cluster.players["theater"]}, plays)
}

// settledAt runs a pass that records the given power in the theater's
// status, then clears the session and apply records of the fake cluster,
// so a test sees only what later passes write.
func settledAt(t *testing.T, cluster *fakeCluster, media *operator, power string) {
	t.Helper()
	cluster.players["theater"].Spec.Power = power
	powerPass(media, cluster, nil)
	mustMatch(t, cluster.players["theater"].Status.Power, specPowerOf(power))
	cluster.sessions, cluster.applies = nil, nil
}

func specPowerOf(power string) string {
	if power == "" {
		return playerPowerOn
	}
	return power
}

// publishedCommands waits for the synctest bubble to settle, then drains
// the broker and returns each published topic and payload, except the
// retained status topics.
func publishedCommands(t *testing.T, broker *fakeBroker) []string {
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

func powerApplied(t *testing.T, cluster *fakeCluster) PlayerCondition {
	t.Helper()
	condition, held := conditionOfType(cluster.players["theater"].Status.Conditions, powerAppliedCondition)
	if !held {
		t.Fatalf("the Player has no PowerApplied condition, conditions are %+v", cluster.players["theater"].Status.Conditions)
	}
	return condition
}

// powerEvents leaves out the Events of the Screen condition.
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

// A Player with an empty status.power records spec.power and sends no
// commands.
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
				mustMatchAll(t, publishedCommands(t, broker), nil)
				mustMatch(t, cluster.sessions[0].session.PowerAsk == nil, true)
				_, held := conditionOfType(cluster.players["theater"].Status.Conditions, powerAppliedCondition)
				mustMatch(t, held, false)
			})
		})
	}
}

// Off writes the receiver's powerAsk of off before the session with
// awake false, so equipment-operator reads the standby request first.
func TestTurningAPlayerOffPowersEverythingDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		media, broker := caughtUpOperator(t, cluster)
		settledAt(t, cluster, media, playerPowerOn)
		publishedCommands(t, broker)
		cluster.players["theater"].Spec.Power = playerPowerOff

		powerPass(media, cluster, runningPlays())

		mustMatch(t, cluster.sessions[0].session.PowerAsk.Action, receiverPowerOff)
		mustMatch(t, cluster.sessions[0].session.Awake, true)
		mustMatch(t, cluster.sessions[len(cluster.sessions)-1].session.Awake, false)
		mustMatch(t, *cluster.displays[testMonitor].Spec.Override, DisplayOverride{Power: displayPowerOff})
		mustMatchAll(t, publishedCommands(t, broker), []string{
			"liken/media/players/house/theater/power " + `{"action":"sleep"}`,
			"liken/media/plays/house/movie/commands " + `{"action":"stop"}`,
		})
		mustMatch(t, cluster.players["theater"].Status.Power, playerPowerOff)
		applied := powerApplied(t, cluster)
		mustMatch(t, applied.Status, conditionTrue)
		mustMatch(t, applied.Reason, powerReasonApplied)
		mustMatch(t, applied.Message, "Off: applied power Off to Display DP-1, requested off from Receiver den-receiver, "+
			"published sleep to the screen, published stop to Play movie")
		mustMatchAll(t, powerEvents(cluster), []string{"Normal Applied: " + applied.Message})
	})
}

// The pass lifts the override at once. The session becomes awake when
// the screen client publishes an on panel desire.
func TestTurningAPlayerOnWakesTheScreenAndLiftsTheOverride(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		media, broker := caughtUpOperator(t, cluster)
		settledAt(t, cluster, media, playerPowerOff)
		publishedCommands(t, broker)
		cluster.players["theater"].Spec.Power = playerPowerOn

		powerPass(media, cluster, nil)

		mustMatch(t, cluster.displays[testMonitor].Spec.Override == nil, true)
		mustMatchAll(t, publishedCommands(t, broker), []string{
			"liken/media/players/house/theater/power " + `{"action":"wake"}`,
		})
		mustMatch(t, cluster.players["theater"].Status.Power, playerPowerOn)
		mustMatch(t, powerApplied(t, cluster).Message,
			"On: removed the override from Display DP-1, published wake to the screen")

		sendPanelDesire(media, panelDesireOn)
		powerPass(media, cluster, nil)

		mustMatch(t, cluster.sessions[len(cluster.sessions)-1].session.Awake, true)
	})
}

// Each change of spec.power posts one Event, though PowerApplied stays
// True with the same reason. A pass that changes nothing posts none.
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
			"Normal Applied: Off: applied power Off to Display DP-1, requested off from Receiver den-receiver, " +
				"published sleep to the screen",
			"Normal Applied: On: removed the override from Display DP-1, published wake to the screen",
		})
	})
}

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
			message:  "Off: applied power Off to Display DP-1, published sleep to the screen",
		},
		{
			power:    playerPowerOn,
			override: "no override",
			ask:      `{"action":"wake"}`,
			message:  "On: removed the override from Display DP-1, published wake to the screen",
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
				publishedCommands(t, broker)
				cluster.players["theater"].Spec.Power = each.power

				powerPass(media, cluster, nil)

				mustMatch(t, describeOverride(cluster.displays[testMonitor].Spec.Override), each.override)
				mustMatchAll(t, publishedCommands(t, broker), []string{
					"liken/media/players/house/theater/power " + each.ask,
				})
				mustMatch(t, powerApplied(t, cluster).Message, each.message)
			})
		})
	}
}

// A Receiver that rejects the powerAsk also rejects the session write,
// which posts its own failure and recovery Events.
func TestAFailedPowerWriteIsRetried(t *testing.T) {
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

// typesOf cuts each Event down to its type and reason, because the
// message of a failed write contains the fake server's error text.
func typesOf(posted []string) []string {
	cut := make([]string, len(posted))
	for index, event := range posted {
		cut[index], _, _ = strings.Cut(event, ":")
	}
	return cut
}

func TestARecordedPowerSendsNoCommandsAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		media, broker := caughtUpOperator(t, cluster)
		settledAt(t, cluster, media, playerPowerOn)
		cluster.players["theater"].Spec.Power = playerPowerOff
		powerPass(media, cluster, nil)
		publishedCommands(t, broker)
		cluster.sessions, cluster.applies = nil, nil

		powerPass(media, cluster, nil)

		mustMatchAll(t, publishedCommands(t, broker), nil)
		mustMatch(t, len(cluster.sessions), 0)
		mustMatch(t, len(cluster.applies), 0)
	})
}

// A retained on panel desire does not wake the equipment of a Player
// that is Off.
func TestAPlayerThatIsOffKeepsTheSessionAsleep(t *testing.T) {
	cluster := receiverCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))
	cluster.players["theater"].Spec.Power = playerPowerOff
	sendPanelDesire(media, panelDesireOn)

	powerPass(media, cluster, nil)

	mustMatch(t, appliedAwake(cluster), "false")
}

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

// The failed change to Off can have turned the receiver off, so the
// pass sends the commands for On although status.power is still On.
func TestAFailedChangeThatIsRevertedStillSendsCommands(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := receiverCluster()
		media, broker := caughtUpOperator(t, cluster)
		settledAt(t, cluster, media, playerPowerOn)
		cluster.players["theater"].Spec.Power = playerPowerOff
		cluster.applyFails = true
		powerPass(media, cluster, nil)
		publishedCommands(t, broker)
		cluster.applyFails = false
		cluster.players["theater"].Spec.Power = playerPowerOn

		powerPass(media, cluster, nil)

		mustMatchAll(t, publishedCommands(t, broker), []string{
			"liken/media/players/house/theater/power " + `{"action":"wake"}`,
		})
		mustMatch(t, cluster.players["theater"].Status.Power, playerPowerOn)
		mustMatch(t, powerApplied(t, cluster).Status, conditionTrue)
	})
}

// clusterWithPower returns a receiver cluster whose theater has the
// given spec.power, the matching status.power, and a new Play.
func clusterWithPower(power string) *fakeCluster {
	cluster := receiverCluster()
	cluster.players["theater"].Spec.Power = power
	cluster.players["theater"].Status.Power = specPowerOf(power)
	cluster.plays["movie"] = housePlay("https://nas/film.mkv")
	return cluster
}

// A Play that starts turns its Player on first, with the same apply as
// a power key, so the session that it applies is awake.
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
			cluster := clusterWithPower(each.power)
			media := testOperator(t, cluster, make(chan struct{}, 1))

			media.pass()

			mustMatchAll(t, cluster.playerApplies, each.applies)
			mustMatch(t, specPower(cluster.players["theater"]), playerPowerOn)
			mustMatch(t, cluster.pods["movie-playback"] != nil, true)
			mustMatch(t, cluster.sessions[0].session.Awake, true)
		})
	}
}

// The pass after the start runs the change to On as it does for a key
// press.
func TestAPlayThatStartsRunsTheChangeToOn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := clusterWithPower(playerPowerOff)
		media, broker := caughtUpOperator(t, cluster)

		media.pass()
		media.pass()

		mustMatch(t, slices.Contains(publishedCommands(t, broker),
			"liken/media/players/house/theater/power "+`{"action":"wake"}`), true)
		mustMatch(t, cluster.players["theater"].Status.Power, playerPowerOn)
		mustMatch(t, powerApplied(t, cluster).Reason, powerReasonApplied)
		mustMatch(t, len(powerEvents(cluster)), 1)
	})
}

func TestARefusedApplyStillStartsThePlay(t *testing.T) {
	cluster := clusterWithPower(playerPowerOff)
	cluster.playerApplyFails = true
	media := testOperator(t, cluster, make(chan struct{}, 1))

	media.pass()

	mustMatch(t, cluster.players["theater"].Spec.Power, playerPowerOff)
	mustMatch(t, cluster.pods["movie-playback"] != nil, true)
	mustMatch(t, appliedAwake(cluster), "false")
}
