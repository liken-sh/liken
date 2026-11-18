package main

// This file connects the volume engine to the rest of the operator.
// Each pass tells the engine which devices control a Player's volume and
// the levels they report. The bus reader passes it each volume key press
// and each message on a Player's volume/commands topic.
//
// When the Player's screen is connected to a Receiver whose Reachable
// condition is True, the Receiver controls the volume. Otherwise each
// of the Player's Sinks does, each on its own scale, and the level
// published on the volume topic is the first Sink's. When the Receiver
// stops responding, the watch delivers its condition change, and the
// next press goes to the Sinks.

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// mainZone is the key in a Receiver's status.zones whose volume is the
// Player's volume.
const mainZone = "main"

// volumeDevices returns the devices that control one Player's volume. A
// Sink that the view cannot read is left out.
func (o *operator) volumeDevices(player *Player, sinks []PlayerSinkStatus) []volumeDevice {
	if receiver, sets := o.levelReceiver(player); sets {
		return []volumeDevice{receiverDevice(receiver)}
	}
	var devices []volumeDevice
	for _, each := range sinks {
		sink, err := o.view.Sink(each.Name)
		if err != nil {
			if !errors.Is(err, apiclient.ErrNotFound) {
				fmt.Fprintf(os.Stderr, "reading Sink %s: %v\n", each.Name, err)
			}
			continue
		}
		devices = append(devices, sinkDevice(sink))
	}
	return devices
}

// levelReceiver returns the Receiver matched to the Player's screen while
// its Reachable condition is True. Passthrough also requires it, because
// a Sink's volume does not change an encoded bitstream, so only the
// Receiver can set the volume.
func (o *operator) levelReceiver(player *Player) (*Receiver, bool) {
	receiver, _, matched := o.matchReceiver(player)
	if !matched || receiverReachable(receiver) != conditionTrue {
		return nil, false
	}
	return receiver, true
}

// driverScaleTops maps a driver name to the top of its volume scale, for
// a Receiver whose spec.volume.max is not set. A WiiM reports volume on
// a fixed scale of 0 to 100. A Denon's reported maximum changes with
// the volume, so it has no entry, and its CRD requires spec.volume.max.
var driverScaleTops = map[string]float64{"wiim": 100}

// receiverDevice builds a volumeDevice from a Receiver's spec.volume and
// its main zone's reported volume and mute. The max is spec.volume.max,
// or else the driver's entry in driverScaleTops. With neither, the max
// is 0 and a press writes nothing. The step defaults to 1 on the
// Receiver's scale. When spec.volume.indicator is Receiver, the screens
// draw no volume bar, because the Receiver draws its own on the TV.
func receiverDevice(receiver *Receiver) volumeDevice {
	device := volumeDevice{kind: deviceReceiver, name: receiver.Metadata.Name, step: 1,
		max: driverScaleTops[receiver.Status.Driver]}
	if receiver.Spec.Volume != nil {
		if receiver.Spec.Volume.Max > 0 {
			device.max = receiver.Spec.Volume.Max
		}
		if receiver.Spec.Volume.Step > 0 {
			device.step = receiver.Spec.Volume.Step
		}
		if receiver.Spec.Volume.Indicator == receiverIndicatorReceiver {
			device.indicator = relayIndicatorReceiver
		}
	}
	zone, reported := receiver.Status.Zones[mainZone]
	if level, err := strconv.ParseFloat(zone.Volume, 64); reported && err == nil {
		device.level, device.mute, device.reported = level, zone.Mute, true
	}
	return device
}

// sinkDevice builds a volumeDevice from a Sink's scale and the volume and
// mute in its status.observed.
func sinkDevice(sink *Sink) volumeDevice {
	device := volumeDevice{kind: deviceSink, name: sink.Metadata.Name}
	device.max, device.step = sinkScale(sink)
	if observed := sink.Status.Observed; observed.Volume != nil {
		device.level, device.reported = float64(*observed.Volume), true
		if observed.Mute != nil {
			device.mute = *observed.Mute
		}
	}
	return device
}

// writeVolumeAsk writes a volume request to the device: into the
// Receiver's session, or into the Sink's session for this Player.
func (o *operator) writeVolumeAsk(unit string, device volumeDevice, ask VolumeAsk) error {
	switch device.kind {
	case deviceReceiver:
		return o.sessions.ask(o.client, device.name, func(asks *receiverAsks) { asks.volume = &ask })
	case deviceSink:
		return ApplySinkSession(o.client, device.name, &SinkSession{
			Player: unit,
			VolumeAsk: &SinkVolumeAsk{
				Level: int(math.Round(ask.Level)),
				Mute:  ask.Mute,
				At:    ask.At,
			},
		})
	}
	return fmt.Errorf("volume device %s has kind %q, want %q or %q", device.name, device.kind, deviceReceiver, deviceSink)
}

// publishLevel publishes a Player's level on its volume topic, retained,
// so a screen that starts later receives the current level.
func (o *operator) publishLevel(unit string, payload []byte) {
	namespace, name, _ := strings.Cut(unit, "/")
	o.bus.Publish(playerVolumeTopic(o.topicBase, namespace, name), payload, true)
}

// volumeKeys maps controller key names to volume changes. A held step
// key moves one step on each repeat. A held mute key acts once, on the
// press, because pressVolume ignores its repeats.
var volumeKeys = map[string]volumeChange{
	"KEY_VOLUMEUP":   {step: 1},
	"KEY_VOLUMEDOWN": {step: -1},
	"KEY_MUTE":       {mute: muteToggle},
	"KEY_UNMUTE":     {mute: muteOff},
}

// pressVolume passes one controller key event to the engine when the
// key is a volume key and the controller's focus is on a Player. A key
// release, value 0, does nothing, and a repeat, value 2, is passed as a
// quiet press that writes no log line.
func (o *operator) pressVolume(namespace, controller string, payload []byte) {
	var event keyEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}
	change, bound := volumeKeys[event.Key]
	if !bound || event.Value == 0 || (event.Value == 2 && change.step == 0) {
		return
	}
	player, unit := o.focusedPlayer(namespace, controller)
	if unit == "" {
		return
	}
	trigger := fmt.Sprintf("remote %s: %s pressed with focus on player %s", controllerKey(namespace, controller), event.Key, player)
	o.levels.press(unit, change, trigger, event.Value == 2)
}

// volumeCommand is the JSON message on a Player's volume/commands topic.
// Step is "up" or "down". Mute is "toggle", true, or false. When both
// are set, the step wins.
type volumeCommand struct {
	Step string          `json:"step,omitempty"`
	Mute json.RawMessage `json:"mute,omitempty"`
}

// parseVolumeCommand returns false for a message that is not valid
// JSON or that has no step or mute that it recognizes.
func parseVolumeCommand(payload []byte) (volumeChange, bool) {
	var command volumeCommand
	if err := json.Unmarshal(payload, &command); err != nil {
		return volumeChange{}, false
	}
	switch command.Step {
	case "up":
		return volumeChange{step: 1}, true
	case "down":
		return volumeChange{step: -1}, true
	}
	switch string(command.Mute) {
	case `"toggle"`:
		return volumeChange{mute: muteToggle}, true
	case "true":
		return volumeChange{mute: muteOn}, true
	case "false":
		return volumeChange{mute: muteOff}, true
	}
	return volumeChange{}, false
}

// commandVolume passes a message from a Player's volume/commands topic
// to the engine as a press.
func (o *operator) commandVolume(namespace, name string, payload []byte) {
	unit := playerKey(namespace, name)
	topic := playerVolumeCommandsTopic(o.topicBase, namespace, name)
	change, ok := parseVolumeCommand(payload)
	if !ok {
		logLine(o.log, "player %s: %s on %s ignored, because it has no step and no mute", unit, payload, topic)
		return
	}
	o.levels.press(unit, change, fmt.Sprintf("player %s: %s on %s", unit, payload, topic), false)
}

func (o *operator) newVolumeEngine() *volumeEngine {
	return newVolumeEngine(o.writeVolumeAsk, o.publishLevel, o.log)
}
