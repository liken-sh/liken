package main

// These tests run the volume engine inside the operator, against the
// fake API server of operate_test.go and the fake broker of bus_test.go.

import (
	"testing"
	"testing/synctest"

	"github.com/liken-sh/liken/kubernetes/informer"
)

// denonReceiver is the house Receiver with a volume scale and a level
// of 45.5. Its InputSelected condition is True, so a press writes no
// input request.
func denonReceiver() *Receiver {
	receiver := houseReceiver()
	receiver.Status.Conditions = append(receiver.Status.Conditions,
		ReceiverCondition{Type: receiverInputSelectedCondition, Status: "True"})
	receiver.Spec.Volume = &ReceiverVolume{Max: 72, Step: 0.5}
	receiver.Status.Zones = map[string]ReceiverZone{mainZone: {Volume: "45.5"}}
	return receiver
}

// frontSink is the house Player's Sink, at a volume of 40.
func frontSink() *Sink {
	level := 40
	return &Sink{Metadata: ObjectMeta{Name: "front"}, Status: SinkStatus{Observed: SinkObserved{Volume: &level}}}
}

// volumeCluster returns a cluster where the house Player's screen is
// connected to denonReceiver and frontSink is in the Player's
// status.sinks.
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

func TestADevicesScaleAndReport(t *testing.T) {
	maxPercent, stepPercent, muted := 80, 2, true
	stated := frontSink()
	stated.Spec.Volume = &SinkVolume{Max: &maxPercent, Step: &stepPercent}
	stated.Status.Observed.Mute = &muted
	plain := houseReceiver()
	plain.Spec.Volume = &ReceiverVolume{Max: 98}
	plain.Status.Zones = map[string]ReceiverZone{mainZone: {Volume: "41", Mute: true}}
	overlay := denonReceiver()
	overlay.Spec.Volume.Indicator = receiverIndicatorReceiver
	cases := []struct {
		name   string
		device volumeDevice
		want   volumeDevice
	}{
		{name: "a Denon", device: receiverDevice(denonReceiver()),
			want: volumeDevice{kind: deviceReceiver, name: "den-receiver", max: 72, step: 0.5, level: 45.5, reported: true}},
		{name: "a receiver with no step", device: receiverDevice(plain),
			want: volumeDevice{kind: deviceReceiver, name: "den-receiver", max: 98, step: 1, level: 41, mute: true, reported: true}},
		{name: "a receiver with its own overlay", device: receiverDevice(overlay),
			want: volumeDevice{kind: deviceReceiver, name: "den-receiver", max: 72, step: 0.5, level: 45.5, reported: true, indicator: "receiver"}},
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

// An audio-operator that serves spec.volume as an integer still gives
// a Sink that converts, with the integer as spec.volume.level.
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

// A volume request on a Receiver is applied together with the session
// that the pass applied, so the apply does not remove either.
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

// A volume request on a Sink rounds the level to a whole number.
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

// A key press, a repeat, and a volume/commands message each move the
// Receiver one step down from the level it reports.
func TestAKeyPressAndAVolumeCommandMoveTheReceiver(t *testing.T) {
	cases := []struct {
		name string
		send func(*operator)
	}{
		{name: "a key", send: func(media *operator) { press(media, "KEY_VOLUMEDOWN", 1) }},
		{name: "a repeat", send: func(media *operator) { press(media, "KEY_VOLUMEDOWN", 2) }},
		{name: "a volume command", send: func(media *operator) {
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

func TestAPressThatMovesNoLevel(t *testing.T) {
	cases := []struct {
		name  string
		focus string
		value int
	}{
		{name: "focus on another unit", focus: "studio", value: 1},
		{name: "a release", focus: "theater", value: 0},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := volumeCluster()
				media := testOperator(t, cluster, make(chan struct{}, 1))
				runPlayers(media, []Player{*cluster.players["theater"]}, nil)
				media.focus.setMark(controllerKey("house", "sofa"), each.focus)
				cluster.sessions = nil

				press(media, "KEY_VOLUMEDOWN", each.value)
				synctest.Wait()

				mustMatch(t, len(cluster.sessions), 0)
			})
		})
	}
}

// The published level is a fraction of the device's max: 36 of 72 is
// 0.5.
func TestAPassPublishesTheUnitsLevel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := volumeCluster()
		cluster.receivers["den-receiver"].Status.Zones[mainZone] = ReceiverZone{Volume: "36"}
		media, broker := caughtUpOperator(t, cluster)

		media.pass()

		published := mustPublishVolume(t, broker)
		mustMatch(t, published.retained, true)
		mustMatch(t, string(published.payload), `{"level":0.5,"muted":false}`)
	})
}

// A pass does not publish a level that the broker already retains, so
// a restart of the operator does not draw a volume bar on the screens.
func TestAPassPublishesNoLevelTheBrokerRetains(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := volumeCluster()
		cluster.receivers["den-receiver"].Status.Zones[mainZone] = ReceiverZone{Volume: "36"}
		media, broker := caughtUpOperator(t, cluster)
		media.handleBusMessage(theaterVolumeTopic(), []byte(`{"level":0.5,"muted":false}`))

		media.pass()

		mustPublishNoVolume(t, broker)
	})
}

// screenAskOn returns a Television whose status.screenAsk, written at
// the given time, requests the given screen for house/theater.
func screenAskOn(at, screen string) *Television {
	return &Television{
		Metadata: ObjectMeta{Name: "den-tv"},
		Status: TelevisionStatus{ScreenAsk: &TelevisionScreenAsk{
			At: at, Player: "house/theater", Screen: screen, Cause: "Set Stream Path",
		}},
	}
}

// The first read only records the request, because it was written
// before the operator started. A later request is published once, not
// retained, however many reads find it.
func TestAScreenRequestIsRelayedOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cluster := newFakeCluster()
		cluster.televisions["den-tv"] = screenAskOn("2026-10-04T12:00:00.000Z", "Wake")
		media, broker := caughtUpOperator(t, cluster)
		media.playerReceivers.set(playerKey("house", "theater"), receiverWiring{name: "den-receiver"})

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
	})
}

// equipment-operator writes a request only for a Player with a
// Receiver, so a request for a Player that has none is late, such as
// one written before the Receiver was deleted, and is not relayed.
func TestAScreenRequestForAUnitWithNoReceiverIsNotRelayed(t *testing.T) {
	cases := []struct {
		name  string
		shape func(*operator)
	}{
		{name: "a unit the pass placed with no receiver", shape: func(media *operator) {
			media.playerReceivers.set(playerKey("house", "theater"), receiverWiring{})
		}},
		{name: "a unit the pass has not placed", shape: func(*operator) {}},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := newFakeCluster()
				cluster.televisions["den-tv"] = screenAskOn("2026-10-04T12:00:00.000Z", "Wake")
				media, broker := caughtUpOperator(t, cluster)
				each.shape(media)
				media.relayScreenAsks()

				cluster.televisions["den-tv"] = screenAskOn("2026-10-04T12:05:00.000Z", "Sleep")
				media.relayScreenAsks()

				mustPublishNothing(t, broker)
			})
		})
	}
}
