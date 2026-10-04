package main

// The volume indicator a Receiver draws itself. A Receiver with
// spec.volume.indicator Receiver shows its own overlay on the TV, so the
// relay marks its level with "indicator": "receiver" and the screens
// track the level and draw no bar. The mark follows the device that sets
// the level now: the sinks carry none.

import (
	"io"
	"net"
	"testing"
)

// denonWithOverlay is the house Denon with its own on-screen overlay.
func denonWithOverlay(level float64) volumeDevice {
	device := denon(level)
	device.indicator = relayIndicatorReceiver
	return device
}

// The relay carries the mark only for a Receiver that draws its own
// indicator.
func TestTheRelayMarksOnlyAReceiverThatDrawsItsOwnIndicator(t *testing.T) {
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
			recorder := &deskRecorder{}
			engine := newVolumeEngine(recorder.write, recorder.publish, io.Discard)

			engine.observe(engineUnit, []volumeDevice{each.device}, true)

			mustMatchAll(t, recorder.relayed(), []string{each.want})
		})
	}
}

// When the Receiver stops setting the level, the sinks set it and the
// relay carries no mark, so the screens draw the bar again.
func TestTheMarkLeavesWithTheReceiver(t *testing.T) {
	engine, recorder := engineFor(denonWithOverlay(36))
	sink := volumeDevice{kind: deviceSink, name: "front", max: 100, step: 5, level: 40, reported: true}

	engine.observe(engineUnit, []volumeDevice{sink}, true)

	mustMatchAll(t, recorder.relayed(), []string{`{"level":0.4,"muted":false}`})
}

// A change of the indicator alone republishes the level, so the
// retained message holds the mark the screens read.
func TestAChangeOfTheIndicatorAloneIsPublished(t *testing.T) {
	engine, recorder := engineFor(denon(36))

	engine.observe(engineUnit, []volumeDevice{denonWithOverlay(36)}, true)
	engine.observe(engineUnit, []volumeDevice{denonWithOverlay(36)}, true)
	engine.observe(engineUnit, []volumeDevice{denon(36)}, true)

	mustMatchAll(t, recorder.relayed(), []string{
		`{"level":0.5,"muted":false,"indicator":"receiver"}`,
		`{"level":0.5,"muted":false}`,
	})
}

// The pass reads spec.volume.indicator off the Receiver that sets the
// level, and off no Receiver once it is unreachable.
func TestAPassMarksTheLevelOfAReceiverWithItsOwnOverlay(t *testing.T) {
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
	}
}

// The sidecar sends the display every live level, with a third word
// that says whether to draw the bar. A marked level draws nothing, and
// neither does a message that changes the mark alone, while the level
// the display holds still follows.
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
			server, client := net.Pipe()
			t.Cleanup(func() { server.Close() })
			lines := readAsync(server)
			c := &commander{
				volumeTopic: playerVolumeTopic(defaultTopicBase, "house", "theater"),
				mpv:         client,
			}
			// An empty retained payload decodes to no level, the way a
			// unit with nothing retained starts.
			c.handleRetained(c.volumeTopic, []byte(each.retained))

			c.handle(c.volumeTopic, []byte(each.live))

			mustMatch(t, waitForLine(t, lines), each.want)
		})
	}
}
