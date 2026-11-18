package main

// A Receiver with spec.volume.indicator set to Receiver draws its own
// volume overlay on the TV. While that Receiver sets the level, the
// relay adds "indicator": "receiver" to the level message, and the
// screens track the level without drawing a bar. A level from the Sinks
// never has the indicator field.

import (
	"io"
	"net"
	"testing"
	"testing/synctest"
)

func denonWithOverlay(level float64) volumeDevice {
	device := denon(level)
	device.indicator = relayIndicatorReceiver
	return device
}

func TestTheIndicatorFieldIsSetOnlyForAReceiverThatDrawsItsOwnOverlay(t *testing.T) {
	cases := []struct {
		name   string
		device volumeDevice
		want   string
	}{
		{name: "a receiver with its own overlay", device: denonWithOverlay(36),
			want: `{"level":0.5,"muted":false,"indicator":"receiver"}`},
		{name: "a receiver the Player draws for", device: denon(36),
			want: `{"level":0.5,"muted":false}`},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			recorder := &volumeWriteRecorder{}
			engine := newVolumeEngine(recorder.write, recorder.publish, io.Discard)

			engine.observe(enginePlayer, []volumeDevice{each.device}, true)

			mustMatchAll(t, recorder.relayed(), []string{each.want})
		})
	}
}

// When the Sinks take over the level from the Receiver, the message has
// no indicator field, so the screens draw the bar again.
func TestALevelFromTheSinksHasNoIndicatorField(t *testing.T) {
	engine, recorder := engineFor(denonWithOverlay(36))
	sink := volumeDevice{kind: deviceSink, name: "front", max: 100, step: 5, level: 40, reported: true}

	engine.observe(enginePlayer, []volumeDevice{sink}, true)

	mustMatchAll(t, recorder.relayed(), []string{`{"level":0.4,"muted":false}`})
}

// A change of the indicator alone republishes the level, so the
// retained message has the current indicator field.
func TestAChangeOfTheIndicatorAloneIsPublished(t *testing.T) {
	engine, recorder := engineFor(denon(36))

	engine.observe(enginePlayer, []volumeDevice{denonWithOverlay(36)}, true)
	engine.observe(enginePlayer, []volumeDevice{denonWithOverlay(36)}, true)
	engine.observe(enginePlayer, []volumeDevice{denon(36)}, true)

	mustMatchAll(t, recorder.relayed(), []string{
		`{"level":0.5,"muted":false,"indicator":"receiver"}`,
		`{"level":0.5,"muted":false}`,
	})
}

// When the Receiver is unreachable, the Sinks set the level, so the
// pass reads no spec.volume.indicator.
func TestAPassSetsTheIndicatorFieldForAReceiverWithItsOwnOverlay(t *testing.T) {
	cases := []struct {
		name      string
		reachable string
		want      string
	}{
		{name: "reachable", reachable: "True", want: `{"level":0.5,"muted":false,"indicator":"receiver"}`},
		{name: "unreachable", reachable: "False", want: `{"level":0.4,"muted":false}`},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				cluster := volumeCluster()
				receiver := cluster.receivers["den-receiver"]
				receiver.Spec.Volume.Indicator = receiverIndicatorReceiver
				receiver.Status.Zones[mainZone] = ReceiverZone{Volume: "36"}
				receiver.Status.Conditions = []ReceiverCondition{
					{Type: receiverReachableCondition, Status: each.reachable},
				}
				media, broker := caughtUpOperator(t, cluster)

				media.pass()

				mustMatch(t, string(mustPublishVolume(t, broker).payload), each.want)
			})
		})
	}
}

// The sidecar sends each live level to the display with a last
// argument that says whether to draw the bar. A level with the
// indicator field draws no bar, and neither does a message that changes
// only the indicator field.
func TestTheSidecarTellsTheDisplayWhetherToDraw(t *testing.T) {
	cases := []struct {
		name     string
		retained string
		live     string
		want     string
	}{
		{name: "a level the Player draws",
			live: `{"level":0.63,"muted":true}`,
			want: `{"command":["script-message","volume-changed","0.63","yes","yes"]}`},
		{name: "a level the receiver draws",
			live: `{"level":0.63,"muted":false,"indicator":"receiver"}`,
			want: `{"command":["script-message","volume-changed","0.63","no","no"]}`},
		{name: "the mark leaves and the level stays",
			retained: `{"level":0.63,"muted":false,"indicator":"receiver"}`,
			live:     `{"level":0.63,"muted":false}`,
			want:     `{"command":["script-message","volume-changed","0.63","no","no"]}`},
		{name: "the mark leaves and the level moves",
			retained: `{"level":0.63,"muted":false,"indicator":"receiver"}`,
			live:     `{"level":0.4,"muted":false}`,
			want:     `{"command":["script-message","volume-changed","0.4","no","yes"]}`},
	}
	for _, each := range cases {
		t.Run(each.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				server, client := net.Pipe()
				t.Cleanup(func() { server.Close() })
				lines := readAsync(server)
				c := &commander{
					volumeTopic: playerVolumeTopic(defaultTopicBase, "house", "theater"),
					mpv:         client,
				}
				// An empty retained payload decodes to no level, the same as
				// no retained message.
				c.handleRetained(c.volumeTopic, []byte(each.retained))

				c.handle(c.volumeTopic, []byte(each.live))

				mustMatch(t, waitForLine(t, lines), each.want)
			})
		})
	}
}
