# The receiver's HTTP interface and the TV wake

Plan 07. The findings are collected and the design is not written.

## The problem

A power press on the room's remote turns the receiver on and selects the
input, and the TV stays dark. The receiver cannot wake it. The receiver
is a CEC responder, and CEC has no receiver-to-TV power-on.

## The second interface

Port 23 is not the whole receiver. The AVR also answers HTTP under
`/goform/`, the interface the Denon phone app uses, and some controls
exist only there.

* `GET /goform/formiPhoneAppPower.xml?1+PowerOn` turns a zone on.
  `2+PowerOn` and `3+PowerOn` name the second and third zone. The same
  paths accept `+PowerStandby`.
* `GET /goform/formiPhoneAppDirect.xml?<code>` sends one remote-button
  code. `RCKSK0410826` turns HDMI Control (CEC) on, and `RCKSK0410827`
  turns it off.

The video controls that the port-23 protocol names (`VSMONI`, `VSAUDIO`,
`VSVPM`) answer on this interface as button codes. The receiver does
answer them, and port 23 does not.

## HDMI Control

The receiver does not originate CEC. Denon's support states it in as
many words: the AVR "does not generate a CEC command, but rather accepts
it and responds". The manual's linked operations agree. They are power
off in both directions, TV-to-receiver input switching, the TV remote's
volume, and ARC. There is no receiver-to-TV power-on, so turning the
receiver on never wakes the TV.

The four items under HDMI Setup are ARC, TV Audio Switching, Pow.Off
Control (All, Video, Off), and Power Saving. The house's receiver has
HDMI Control on, ARC on, TV Audio Switching off, Pow.Off Control set to
All, and Power Saving off.

## The design, not written

A TV wakes only when a device in the room originates CEC. The liken
machines carry no CEC on their HDMI ports, so an adapter is the answer.
Plan 05 leaves the shape of that node-attached component open.

This plan owes two things. The first is that shape, and the node that
carries the adapter. The second is the driver's reach into the HTTP
interface above, and which of its controls the driver takes.

## What was considered and set aside

* **The PlayStation 5's CEC.** It woke the room and took the display,
  which is why CEC was turned down. Its own setting is the fix:
  "Enable HDMI Device Link" off leaves the receiver's links to the TV
  alone.
* **Home Assistant.** It reads the TV as a cast and Android TV device.
  Its power control is the Google TV's own CEC and not a direct hand,
  and this house operates below that layer.
* **The receiver's HDMI Control setting.** It stays on. It carries the
  power-off links, ARC, and the TV remote's volume, and it does not stop
  the wake.

## Verification

Not run. The plan is proved when the room's power press leaves the
receiver on the session's input and the TV lit.
