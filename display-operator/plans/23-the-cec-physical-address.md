# The CEC physical address

Plan 23. Proposed 2026-09-26.

Each connected `Display` reports the HDMI-CEC physical address that
its EDID gives the machine's port. The operator reads the address
from the EDID's HDMI vendor-specific data block, publishes it as
`status.physicalAddress`, and adds it to the output device's slice
attributes. equipment-operator's CEC support reads it, because a CEC
adapter announces this address when it speaks for the machine that
drives the `Display`.

## The problem

CEC is the control channel that every HDMI port carries. Every device
on a CEC bus has a physical address: four hex digits that give the
path from the TV to the device, one digit for each HDMI port on the
way. The TV is `0.0.0.0`. A receiver on the TV's HDMI input 1 is
`1.0.0.0`, and a machine on that receiver's input 3 is `1.3.0.0`.
When a device broadcasts `Active Source` with its address, the
receiver and the TV switch their inputs to that path.

A USB-CEC adapter, such as the
[Pulse-Eight](https://www.pulse-eight.com/p/104/usb-hdmi-cec-adapter),
cannot find an address for itself. It has no EDID to read, and its
driver starts with the invalid address `f.f.f.f`. Pulse-Eight's
[two-cable setup](https://support.pulse-eight.com/support/solutions/articles/30000022906-usb-cec-adapter-4k-resolution)
also puts the adapter on a spare HDMI input, out of the video path,
because the adapter passes 4K at 60 Hz only in 4:2:0 color. On that
spare input, the adapter's own port is not the one that matters. For
"show this machine", the adapter must announce the address of the
port where the machine's video enters, so that `Active Source`
switches the receiver and the TV to the machine's picture.

The sink already states that address. The HDMI specification
requires a sink to serve each of its HDMI ports an EDID whose HDMI
vendor-specific data block carries that port's physical address, and
a receiver writes the address of each of its inputs into the EDID it
serves on that input. The operator reads that EDID already. Only the
parse of the vendor block is missing.

[Plan 11](completed/11-darkening-respects-the-attached-input.md)
built that parse, and
[plan 13](completed/13-the-attached-input-retires.md) removed it
with the guard it fed. Plan 13's reason was that nothing else read
the value. The CEC support is a reader.

## The design

**The parse.** `edid.go` gains `PhysicalAddress`, read by the walk
plan 11 built (commit `0ba36f4`, removed in `d27880e`). The walk
steps through the CTA-861 data block collection of each extension
block. A block with tag 3 and the IEEE OUI `00-0C-03`, stored least
significant byte first as `03 0c 00`, is the HDMI vendor block, and
the two bytes after the OUI are the address as four nibbles, `A.B.C.D`.

Plan 11 kept only the form `N.0.0.0`, because it read a port of the
sink itself. This plan keeps every valid address, because a machine
behind a receiver has an address two levels deep. An address is
valid when it is neither `0.0.0.0` nor `f.f.f.f`, and when no
non-zero digit follows a zero digit. `0.0.0.0` is the TV's own
address, and a sink serves it when it states no address. `f.f.f.f`
is the invalid address. A digit after a zero names no path.

**The status.** `status.physicalAddress` holds the address in the
dotted form CEC tools print, for example `1.3.0.0`. A new condition,
`PhysicalAddressCurrent`, states where the value came from:

- `True`, reason `ReadFromEDID`: the connector's current EDID serves
  this address.
- `False`, reason `Retained`: the connector is disconnected, or its
  current EDID serves no valid address. The field keeps the last
  valid address, and the message says so, for example:
  `HDMI-A-2 serves no EDID; this is the address it served at 21:14:03`.
- The condition is absent while the `Display` has never served a
  valid address, for example on a DisplayPort monitor.

The operator keeps the last address because a receiver in standby
can stop serving its EDID. The machine's port does not move while
the receiver sleeps, and the CEC adapter needs the address most at
that moment: waking the receiver and the TV is the reason it exists.
`status` records the fact, and the reader decides whether a retained
address is good enough for what it does.

A receiver in standby can also pass the TV's own EDID through to the
machine. That EDID has a different manufacturer and model, so it
belongs to a different `Display`, and the receiver's `Display` keeps
its retained address.

**The slice.** The output device gains a `physicalAddress` attribute
beside `manufacturer`, `model`, and `serial`. The attribute follows
the current EDID only. A device the scheduler allocates describes the
hardware as it is now, so a retained value stays in `status`.

**What the operator does not do.** It does not open a CEC adapter,
send a CEC message, or read a CEC bus. It reports the fact that the
EDID states, and equipment-operator owns everything that speaks CEC
([plan 09](https://github.com/liken-sh/equipment-operator/blob/main/plans/09-cec.md)).
The adapter on the machine comes from `liken`'s
[milestone 70](https://github.com/liken-sh/liken/blob/main/plans/70-init-attaches-serio-devices.md).

**Two connectors to one receiver.** Pulse-Eight's two-cable setup
connects one machine to one receiver with two cables: one carries
the picture, and one passes through the CEC adapter. A drill on
2026-09-26 found that both connectors then read the receiver's EDID,
with a different physical address on each: `1.2.0.0` and `1.1.0.0`.
The operator names a `Display` from the EDID's manufacturer, product,
and model, so both connectors map to one `Display` name, and the
cluster showed one `Display` for the two. The address this plan
publishes is per connector, so the plan must give each connector its
own `Display` in that case, or publish the address per connector
under one `Display`. The build decides which, and it states the rule
in the reference.

## What was considered and set aside

**equipment-operator reads the EDID itself.** The CEC pod could read
`/sys/class/drm/*/edid` on its node. It would parse EDIDs a second
time, and it would need to match a connector to a `Display` name,
which is this operator's work. The address is a fact about the
monitor at the end of the cable, and this operator publishes the
other facts about that monitor.

**A declared address on the equipment side.** A person could type
the address in the CEC configuration. The address changes when a
cable moves to another input, and a typed value would then send
`Active Source` to the wrong port with no error. The EDID is the one
source that changes with the cable.

**Only the `N.0.0.0` form.** That was plan 11's rule, and it drops
every machine behind a receiver, which is the main case for CEC.

## How the work is proved

The unit tests extend plan 11's EDID fixtures with a vendor block at
each depth, the two invalid addresses, a digit after a zero, a
truncated block, and an extension with no vendor block.

On hardware:

- On liken-1, the two test panels report `status.physicalAddress` `2.0.0.0` and
  `1.0.0.0`, the addresses plan 11 measured on the same cables, and
  `PhysicalAddressCurrent` is `True`.
- A machine behind a receiver reports a two-level address, and the
  digits match the TV input and the receiver input by eye.
- With the receiver in standby, the drill records what the machine's
  connector serves: no EDID, the receiver's EDID, or the TV's. The
  receiver's `Display` must report `Retained` with the last address
  in every case.
- `cec-ctl` from [v4l-utils](https://git.linuxtv.org/v4l-utils.git),
  on a machine with a CEC adapter, sets the adapter's address to the
  published value and sends `Active Source`, and the receiver and
  the TV switch to that machine's picture.

## References

- The HDMI 1.3a specification, with the CEC supplement, is free from
  [hdmi.org](https://www.hdmi.org/spec/index) after a form. Section
  8.7 defines the physical address and the rule that a sink writes
  each port's address into that port's EDID. Later HDMI versions are
  available only to adopters.
- The kernel's [CEC introduction](https://docs.kernel.org/userspace-api/media/cec/cec-intro.html)
  describes the addresses and the kernel's CEC API.
