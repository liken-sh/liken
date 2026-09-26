# The receiver's HTTP interface and the TV wake

Plan 07. The findings are collected and the design is not written.

## The problem

When a person presses the power button on the remote, the receiver
turns on and selects the input, and the TV stays off. The receiver
cannot wake the TV. The receiver is a CEC responder, and CEC has no
receiver-to-TV power-on.

## The second interface

Port 23 does not expose every control. The AVR also responds to HTTP
under `/goform/`, the interface the Denon phone app uses, and some
controls exist only there.

* `GET /goform/formiPhoneAppPower.xml?1+PowerOn` turns a zone on.
  `2+PowerOn` and `3+PowerOn` name the second and third zone. The same
  paths accept `+PowerStandby`.
* `GET /goform/formiPhoneAppDirect.xml?<code>` sends one remote-button
  code. `RCKSK0410826` turns HDMI Control (CEC) on, and `RCKSK0410827`
  turns it off.

The port-23 protocol names the video controls `VSMONI`, `VSAUDIO`, and
`VSVPM`. The receiver responds to them as button codes on the HTTP
interface, and it does not respond to them on port 23.

## HDMI Control

The receiver does not originate CEC. Denon's support states this
directly: the AVR "does not generate a CEC command, but rather accepts
it and responds". The manual's linked operations agree. They are power
off in both directions, TV-to-receiver input switching, the TV remote's
volume, and ARC. There is no receiver-to-TV power-on, so turning the
receiver on never wakes the TV.

The four items under HDMI Setup are ARC, TV Audio Switching, Pow.Off
Control (All, Video, Off), and Power Saving. The house's receiver has
HDMI Control on, ARC on, TV Audio Switching off, Pow.Off Control set to
All, and Power Saving off.

## The design, still to write

A TV wakes only when a device connected to it originates CEC. The
`liken` machines have no CEC on their HDMI ports, so the wake needs a
CEC adapter. Plan 05 leaves the design of that node-attached component
open.

This plan must decide two things. The first is that design, and the
node that the adapter connects to. The second is how the driver uses
the HTTP interface above, and which of its controls the driver takes.

## What was considered and set aside

* **The PlayStation 5's CEC.** It woke the equipment and switched the
  display to its own input, which is why CEC was turned down. Its own
  setting fixes this: with "Enable HDMI Device Link" off, the
  PlayStation 5 does not interfere with the receiver's links to the TV.
* **Home Assistant.** It reads the TV as a cast and Android TV device.
  Its power control uses the Google TV's own CEC and does not control
  the TV directly, and this house operates below that layer.
* **The receiver's HDMI Control setting.** It stays on. It carries the
  power-off links, ARC, and the TV remote's volume, and it does not
  block the wake.

## Verification

Not run. The plan is proved when a press of the remote's power button
leaves the receiver on the session's input and the TV on.
