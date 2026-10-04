package main

// The volume engine inside the operator: the devices the pass chooses
// for a unit, the asks it writes into the Receiver and the Sinks, the
// relay a pass publishes, and the presses and asks off the bus. The fake
// API server and the fake broker are in operate_test.go and bus_test.go.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// denonReceiver is the house Receiver with a volume scale, reporting a
// level of 45.5 on the unit's input, so a press writes no input ask.
func denonReceiver() *Receiver {
	receiver := houseReceiver()
	receiver.Status.Conditions = append(receiver.Status.Conditions,
		ReceiverCondition{Type: receiverInputSelectedCondition, Status: "True"})
	receiver.Spec.Volume = &ReceiverVolume{Max: 72, Step: 0.5}
	receiver.Status.Zones = map[string]ReceiverZone{mainZone: {Volume: "45.5"}}
	return receiver
}

// frontSink is the Sink the house unit's first spec.sinks entry
// resolved to, reporting 40 percent.
func frontSink() *Sink {
	level := 40
	return &Sink{Metadata: ObjectMeta{Name: "front"}, Status: SinkStatus{Observed: SinkObserved{Volume: &level}}}
}

// volumeCluster is the house unit wired into the Denon, with the front
// Sink remembered on the Player.
func volumeCluster() *fakeCluster {
	cluster := receiverCluster()
	cluster.receivers["den-receiver"] = denonReceiver()
	cluster.sinks["front"] = frontSink()
	cluster.players["theater"].Status.Sinks = []PlayerSinkStatus{{Request: "audio0", Name: "front"}}
	return cluster
}

func houseSinks() []PlayerSinkStatus {
	return []PlayerSinkStatus{{Request: "audio0", Name: "front"}}
}

// The Receiver sets the unit's level while its Reachable condition is
// True. Otherwise every Sink the unit plays through sets it.
func TestTheDevicesFollowTheReceiversReachableCondition(t *testing.T) {
	cases := []struct {
		name      string
		reachable string
		absent    bool
		want      string
	}{
		{name: "reachable", reachable: "True", want: "receiver/den-receiver"},
		{name: "unreachable", reachable: "False", want: "sink/front"},
		{name: "not reported", reachable: "", want: "sink/front"},
		{name: "no receiver", absent: true, want: "sink/front"},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := volumeCluster()
			cluster.receivers["den-receiver"].Status.Conditions = []ReceiverCondition{
				{Type: receiverReachableCondition, Status: each.reachable},
			}
			cluster.receiversAbsent = each.absent
			media := testOperator(t, cluster, make(chan struct{}, 1))

			devices := media.volumeDevices(cluster.players["theater"], houseSinks())

			mustMatch(t, len(devices), 1)
			mustMatch(t, devices[0].key(), each.want)
		})
	}
}

// Each device's scale and report reach the engine in the device's own
// units, with the defaults for what a spec leaves out.
func TestADevicesScaleAndReport(t *testing.T) {
	maxPercent, stepPercent, muted := 80, 2, true
	stated := frontSink()
	stated.Spec.Volume = &SinkVolume{Max: &maxPercent, Step: &stepPercent}
	stated.Status.Observed.Mute = &muted
	plain := houseReceiver()
	plain.Spec.Volume = &ReceiverVolume{Max: 98}
	plain.Status.Zones = map[string]ReceiverZone{mainZone: {Volume: "41", Mute: true}}
	cases := []struct {
		name   string
		device volumeDevice
		want   volumeDevice
	}{
		{name: "a Denon", device: receiverDevice(denonReceiver()),
			want: volumeDevice{kind: deviceReceiver, name: "den-receiver", max: 72, step: 0.5, level: 45.5, reported: true}},
		{name: "a receiver with no step", device: receiverDevice(plain),
			want: volumeDevice{kind: deviceReceiver, name: "den-receiver", max: 98, step: 1, level: 41, mute: true, reported: true}},
		{name: "a receiver with no report", device: receiverDevice(houseReceiver()),
			want: volumeDevice{kind: deviceReceiver, name: "den-receiver", step: 1}},
		{name: "a sink with no scale", device: sinkDevice(frontSink()),
			want: volumeDevice{kind: deviceSink, name: "front", max: 100, step: 5, level: 40, reported: true}},
		{name: "a sink with a scale", device: sinkDevice(stated),
			want: volumeDevice{kind: deviceSink, name: "front", max: 80, step: 2, level: 40, mute: true, reported: true}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			mustMatch(t, each.device, each.want)
		})
	}
}

// A Receiver that states no spec.volume.max steps against the top of its
// driver's own scale: 100 for a WiiM. A driver with no fixed top, or no
// driver yet, gives no max, and a press writes nothing. A stated max
// wins, and a step the spec leaves out is one unit.
func TestAReceiversMaxFallsBackToItsDriversScale(t *testing.T) {
	cases := []struct {
		name     string
		driver   string
		volume   *ReceiverVolume
		wantMax  float64
		wantStep float64
	}{
		{name: "a WiiM with a step alone", driver: "wiim", volume: &ReceiverVolume{Step: 2}, wantMax: 100, wantStep: 2},
		{name: "a WiiM with no volume block", driver: "wiim", wantMax: 100, wantStep: 1},
		{name: "a WiiM with a stated max", driver: "wiim", volume: &ReceiverVolume{Max: 60}, wantMax: 60, wantStep: 1},
		{name: "a Denon with a stated max", driver: "denon", volume: &ReceiverVolume{Max: 72, Step: 0.5}, wantMax: 72, wantStep: 0.5},
		{name: "a Denon with no max", driver: "denon", wantMax: 0, wantStep: 1},
		{name: "no driver yet", wantMax: 0, wantStep: 1},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			receiver := houseReceiver()
			receiver.Status.Driver = each.driver
			receiver.Spec.Volume = each.volume

			device := receiverDevice(receiver)

			mustMatch(t, device.max, each.wantMax)
			mustMatch(t, device.step, each.wantStep)
		})
	}
}

// An audio operator that still serves the integer spec.volume leaves a
// Sink the view converts, with the integer as the resting level.
func TestASinkWithAnIntegerVolumeConverts(t *testing.T) {
	object := asUnstructured(map[string]any{
		"apiVersion": sinkAPIVersion, "kind": "Sink",
		"metadata": map[string]any{"name": "front"},
		"spec":     map[string]any{"volume": 60},
	})

	sink, err := informer.Convert[Sink](object)

	mustSucceed(t, err)
	mustMatch(t, *sink.Spec.Volume.Level, 60)
	maxPercent, step := sinkScale(&sink)
	mustMatch(t, maxPercent, 100.0)
	mustMatch(t, step, 5.0)
}

// A volume ask on a Receiver goes into the session block beside the
// session the pass applied, so the apply removes none of it.
func TestAVolumeAskKeepsTheReceiversSession(t *testing.T) {
	media, cluster := askOperator(t)

	err := media.writeVolumeAsk("house/theater", receiverDevice(denonReceiver()),
		VolumeAsk{Level: 45, At: "2026-10-04T12:15:25.164Z"})

	mustSucceed(t, err)
	mustMatch(t, len(cluster.sessions), 1)
	applied := cluster.sessions[0]
	mustMatch(t, applied.manager, applyFieldManager)
	mustMatch(t, applied.session.Player, "house/theater")
	mustMatch(t, applied.session.Input, "GAME")
	mustMatch(t, *applied.session.VolumeAsk, VolumeAsk{Level: 45, At: "2026-10-04T12:15:25.164Z"})
}

// A volume ask on a Sink names the Player whose ask it is, and carries
// the level in whole percent.
func TestAVolumeAskOnASinkNamesThePlayer(t *testing.T) {
	cluster := volumeCluster()
	media := testOperator(t, cluster, make(chan struct{}, 1))

	err := media.writeVolumeAsk("house/theater", sinkDevice(frontSink()),
		VolumeAsk{Level: 44.6, Mute: true, At: "2026-10-04T12:15:25.164Z"})

	mustSucceed(t, err)
	mustMatch(t, len(cluster.sinkSessions), 1)
	applied := cluster.sinkSessions[0]
	mustMatch(t, applied.name, "front")
	mustMatch(t, applied.manager, applyFieldManager)
	mustMatch(t, applied.session.Player, "house/theater")
	mustMatch(t, *applied.session.VolumeAsk, SinkVolumeAsk{Level: 45, Mute: true, At: "2026-10-04T12:15:25.164Z"})
}

// A press of a volume key on the controller that drives the unit, and a
// message on the unit's volume/commands topic, each move the Receiver
// one step from its report.
func TestAPressAndAnAskReachTheReceiver(t *testing.T) {
	cases := []struct {
		name string
		send func(*operator)
	}{
		{name: "a key", send: func(media *operator) { press(media, "KEY_VOLUMEDOWN", 1) }},
		{name: "a repeat", send: func(media *operator) { press(media, "KEY_VOLUMEDOWN", 2) }},
		{name: "an ask", send: func(media *operator) {
			media.handleBusMessage(playerVolumeCommandsTopic(defaultTopicBase, "house", "theater"), []byte(`{"step":"down"}`))
		}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := volumeCluster()
				media := testOperator(t, cluster, make(chan struct{}, 1))
				runPlayers(media, []Player{*cluster.players["theater"]}, nil)
				media.focus.setMark(controllerKey("house", "sofa"), "theater")
				cluster.sessions = nil

				each.send(media)
				synctest.Wait()

				mustMatch(t, len(cluster.sessions), 1)
				mustMatch(t, cluster.sessions[0].session.VolumeAsk.Level, 45.0)
			})
		})
	}
}

// A press on a controller pointed at another unit, and the release of a
// key, move nothing.
func TestAPressThatMovesNoLevel(t *testing.T) {
	cases := []struct {
		name  string
		mark  string
		value int
	}{
		{name: "a mark on another unit", mark: "studio", value: 1},
		{name: "a release", mark: "theater", value: 0},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := volumeCluster()
				media := testOperator(t, cluster, make(chan struct{}, 1))
				runPlayers(media, []Player{*cluster.players["theater"]}, nil)
				media.focus.setMark(controllerKey("house", "sofa"), each.mark)
				cluster.sessions = nil

				press(media, "KEY_VOLUMEDOWN", each.value)
				synctest.Wait()

				mustMatch(t, len(cluster.sessions), 0)
			})
		})
	}
}

// A pass after the broker's catch-up publishes the unit's level,
// retained, as a fraction of the device's max.
func TestAPassPublishesTheUnitsLevel(t *testing.T) {
	cluster := volumeCluster()
	cluster.receivers["den-receiver"].Status.Zones[mainZone] = ReceiverZone{Volume: "36"}
	media, broker := caughtUpOperator(t, cluster)

	media.pass()

	published := mustPublishVolume(t, broker)
	mustMatch(t, published.retained, true)
	mustMatch(t, string(published.payload), `{"level":0.5,"muted":false}`)
}

// A level the broker already holds is not published again, so an
// operator restart draws no indicator on the screens.
func TestAPassPublishesNoLevelTheBrokerHolds(t *testing.T) {
	cluster := volumeCluster()
	cluster.receivers["den-receiver"].Status.Zones[mainZone] = ReceiverZone{Volume: "36"}
	media, broker := caughtUpOperator(t, cluster)
	media.handleBusMessage(theaterVolumeTopic(), []byte(`{"level":0.5,"muted":false}`))

	media.pass()

	mustPublishNoVolume(t, broker)
}

// A power ask on the unit's power topic goes into the session's
// powerAsk. The wake and sleep this operator publishes there for the
// screen are not power asks, and a unit with no Receiver has no room to
// turn.
func TestAPowerAskReachesTheReceiver(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		shape   func(*operator)
		want    string
	}{
		{name: "toggle", payload: `{"action":"toggle"}`, want: "toggle"},
		{name: "on", payload: `{"action":"on"}`, want: "on"},
		{name: "off", payload: `{"action":"off"}`, want: "off"},
		{name: "a wake for the screen", payload: `{"action":"wake"}`, want: "none"},
		{name: "a sleep for the screen", payload: `{"action":"sleep"}`, want: "none"},
		{name: "no receiver", payload: `{"action":"toggle"}`, want: "none", shape: func(media *operator) {
			media.ensure.set(playerKey("house", "theater"), unitReceiver{})
		}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			media, cluster := askOperator(t)
			if each.shape != nil {
				each.shape(media)
			}

			media.handleBusMessage(playerPowerTopic(defaultTopicBase, "house", "theater"), []byte(each.payload))

			got := "none"
			if len(cluster.sessions) == 1 {
				got = cluster.sessions[0].session.PowerAsk.Action
			}
			mustMatch(t, got, each.want)
		})
	}
}

// screenAskOn is a Television whose node workload asked for the house
// theater's screen at the given time.
func screenAskOn(at, screen string) *Television {
	return &Television{
		Metadata: ObjectMeta{Name: "den-tv"},
		Status: TelevisionStatus{ScreenAsk: &TelevisionScreenAsk{
			At: at, Player: "house/theater", Screen: screen, Cause: "Set Stream Path",
		}},
	}
}

// An ask the first pass finds is older than this operator's start, so
// the pass relays nothing for it. Each new ask after that goes once to
// the Player's power topic, not retained.
func TestTheScreenAsksAreRelayedOnce(t *testing.T) {
	cluster := newFakeCluster()
	cluster.televisions["den-tv"] = screenAskOn("2026-10-04T12:00:00.000Z", "Wake")
	media, broker := caughtUpOperator(t, cluster)
	media.ensure.set(playerKey("house", "theater"), unitReceiver{name: "den-receiver"})

	media.relayScreenAsks()
	mustPublishNothing(t, broker)

	cluster.televisions["den-tv"] = screenAskOn("2026-10-04T12:05:00.000Z", "Sleep")
	media.relayScreenAsks()
	media.relayScreenAsks()

	published := waitForPublish(t, broker.pubs)
	mustMatch(t, published.topic, playerPowerTopic(defaultTopicBase, "house", "theater"))
	mustMatch(t, string(published.payload), `{"action":"sleep"}`)
	mustMatch(t, published.retained, false)
	mustPublishNothing(t, broker)
}

// A screen client in the screen mode handles power itself, so an ask for
// a unit whose screen is wired through no Receiver reaches no screen. A
// late ask after the Receiver is removed is the case this guards.
func TestAScreenAskForAUnitWithNoReceiverIsNotRelayed(t *testing.T) {
	cases := []struct {
		name  string
		shape func(*operator)
	}{
		{name: "a unit the pass placed with no receiver", shape: func(media *operator) {
			media.ensure.set(playerKey("house", "theater"), unitReceiver{})
		}},
		{name: "a unit the pass has not placed", shape: func(*operator) {}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			cluster := newFakeCluster()
			cluster.televisions["den-tv"] = screenAskOn("2026-10-04T12:00:00.000Z", "Wake")
			media, broker := caughtUpOperator(t, cluster)
			each.shape(media)
			media.relayScreenAsks()

			cluster.televisions["den-tv"] = screenAskOn("2026-10-04T12:05:00.000Z", "Sleep")
			media.relayScreenAsks()

			mustPublishNothing(t, broker)
		})
	}
}
