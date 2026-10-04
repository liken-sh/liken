package main

// Where the volume engine's input comes from and where its asks go. The
// pass chooses the devices that set each unit's level and hands their
// reports to the engine. The bus reader hands it each press and each
// message on a Player's volume/commands topic.
//
// A unit's level is set by the Receiver its cable lands on, while the
// Receiver's Reachable condition is True. Otherwise every Sink the unit
// plays through sets it, each in its own units, and the topic carries
// the first Sink in spec.sinks order. A Receiver that stops answering
// is a change on the watch, so the next press goes to the Sinks.

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

// mainZone is the zone a Receiver reports the unit's room in.
const mainZone = "main"

// volumeDevices chooses the devices that set one unit's level, from the
// Receiver the unit's screen matches and the Sinks the unit plays
// through.
func (o *operator) volumeDevices(player *Player, sinks []PlayerSinkStatus) []volumeDevice {
	if receiver, _, matched := o.matchReceiver(player); matched && receiverReachable(receiver) == conditionTrue {
		return []volumeDevice{receiverDevice(receiver)}
	}
	var devices []volumeDevice
	for _, each := range sinks {
		sink, err := o.view.Sink(each.Name)
		if err != nil {
			if !errors.Is(err, apiclient.ErrNotFound) {
				fmt.Fprintf(os.Stderr, "reading sink %s: %v\n", each.Name, err)
			}
			continue
		}
		devices = append(devices, sinkDevice(sink))
	}
	return devices
}

// driverScaleTops is the top of each driver's own volume scale, for a
// Receiver whose spec states no max. A WiiM reports a fixed 0 to 100
// scale, so 100 is its top, the ceiling the equipment operator maps
// against too. A Denon reports a limit that moves with the volume, so
// it has no fixed top, and its CRD requires spec.volume.max.
var driverScaleTops = map[string]float64{"wiim": 100}

// receiverDevice reads a Receiver's scale and its main zone's report.
// The max is spec.volume.max, or the top of the driver's own scale
// (status.driver) when the spec states none. A Receiver with neither
// has no max, and a press writes nothing for it. A step the spec leaves
// out is one unit of the receiver's scale, the step the equipment
// operator takes too.
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
	}
	zone, reported := receiver.Status.Zones[mainZone]
	if level, err := strconv.ParseFloat(zone.Volume, 64); reported && err == nil {
		device.level, device.mute, device.reported = level, zone.Mute, true
	}
	return device
}

// sinkDevice reads a Sink's scale and its report, in percent.
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

// writeVolumeAsk writes one ask into a device's object.
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
	return fmt.Errorf("no device of kind %q", device.kind)
}

// publishLevel puts one level on a unit's topic, retained, so a screen
// that starts reads the level from the broker.
func (o *operator) publishLevel(unit string, payload []byte) {
	namespace, name, _ := strings.Cut(unit, "/")
	o.bus.Publish(playerVolumeTopic(o.topicBase, namespace, name), payload, true)
}

// The volume keys. A press and a repeat of a step key each move the
// target, because each repeat is one step. The mute keys act on the
// press alone, so a held mute toggles once.
var volumeKeys = map[string]volumeChange{
	"KEY_VOLUMEUP":   {step: 1},
	"KEY_VOLUMEDOWN": {step: -1},
	"KEY_MUTE":       {mute: muteToggle},
	"KEY_UNMUTE":     {mute: muteOff},
}

// pressVolume hands one controller event to the engine, while the
// controller's mark names a unit.
func (o *operator) pressVolume(namespace, controller string, payload []byte) {
	var event keyEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return
	}
	change, bound := volumeKeys[event.Key]
	if !bound || event.Value == 0 || (event.Value == 2 && change.step == 0) {
		return
	}
	player, unit := o.focusedUnit(namespace, controller)
	if unit == "" {
		return
	}
	trigger := fmt.Sprintf("remote %s: %s pressed with focus on player %s", controllerKey(namespace, controller), event.Key, player)
	o.levels.press(unit, change, trigger, event.Value == 2)
}

// volumeCommand is one message on a Player's volume/commands topic. Mute
// is "toggle", true, or false.
type volumeCommand struct {
	Step string          `json:"step,omitempty"`
	Mute json.RawMessage `json:"mute,omitempty"`
}

// parseVolumeCommand reads one message as a change.
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

// commandVolume hands one message on a Player's volume/commands topic to
// the engine, the same as a press.
func (o *operator) commandVolume(namespace, name string, payload []byte) {
	unit := playerKey(namespace, name)
	topic := playerVolumeCommandsTopic(o.topicBase, namespace, name)
	change, ok := parseVolumeCommand(payload)
	if !ok {
		logLine(o.log, "player %s: %s on %s ignored, because it names no step and no mute", unit, payload, topic)
		return
	}
	o.levels.press(unit, change, fmt.Sprintf("player %s: %s on %s", unit, payload, topic), false)
}

// newVolumeEngine builds the engine that writes through this
// operator's client and publishes on its bus.
func (o *operator) newVolumeEngine() *volumeEngine {
	return newVolumeEngine(o.writeVolumeAsk, o.publishLevel, o.log)
}
