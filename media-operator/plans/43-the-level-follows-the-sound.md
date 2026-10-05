# 43, The level follows the sound

Proposed on 2026-10-04. Not built. A volume press sets the level on
the device that plays the unit's sound. Today it sets the level on the
`Receiver` whenever the unit's screen is wired through one, even when
the sound plays somewhere else.

## The problem

`volumeDevices` (`volumedevices.go`) chooses the devices that set one
unit's level. It returns the `Receiver` the unit's screen matches,
while that `Receiver` is `Reachable`. Only when no reachable `Receiver`
matches does it return the unit's `Sink` objects.

The screen alone decides. So in a room where the picture goes through
the receiver and the sound plays on a Bluetooth speaker, a volume press
moves the receiver, and the speaker that plays the sound does not
change. The same happens when the unit's sound goes to the machine's
analog jack or to a USB DAC while the picture goes through the
receiver.

Power and input are correct in that room. They follow the picture,
because the receiver carries the picture to the TV. Only the level
follows the wrong path.

## The design

**The `Receiver` sets the level only when it carries the unit's
sound.** It carries the sound when one of the unit's sinks is an HDMI
output whose `monitor.liken.sh/id` attribute equals the monitor id of
the unit's screen. audio-operator publishes that attribute from the
output's ELD block (`addMonitorAttributes` in
`audio-operator/devices.go`), and display-operator publishes the
screen's monitor id from the same EDID. A receiver in the path presents
its own EDID and ELD, so both ids name the receiver.

`volumeDevices` then reads two facts where it reads one today:

1. The `Receiver` that the unit's screen matches, as now.
2. Whether any of the unit's allocated sinks names the same monitor id.

| Screen matches a reachable `Receiver` | A sink names the screen's monitor | The level is set on |
|---|---|---|
| yes | yes | the `Receiver` |
| yes | no | the `Sink` objects |
| no | either | the `Sink` objects |

The Bluetooth speaker carries no monitor id, so it never names the
screen's monitor, and the level goes to its `Sink`. A unit that plays
through both the receiver's HDMI output and a second sink sets the
level on the `Receiver`, because the receiver carries the main sound.

The power and input paths do not change. They keep reading the screen
alone.

## What the design must answer

* `screenOf` (`screen.go`) reads the monitor id off the allocated
  display device. The sinks need the same read from the allocated audio
  devices. Check that the claim's allocation exposes the sink device's
  attributes to `media-operator` the same way, or that `Player.status.sinks`
  can carry the id.
* A receiver in standby can stop presenting its ELD. Check whether the
  sink's attribute stays published while the receiver sleeps, so that a
  press in that window does not move to the sinks.

## How it will be proved

A table test of `volumeDevices` covers the three rows above, with a
`Receiver`, a `Display`, and a sink device built from fixtures. A
fourth case covers a unit with both an HDMI sink on the receiver and a
Bluetooth sink, and expects the `Receiver`.
