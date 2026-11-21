## The name

The operator builds the name from the hardware's identity, so the name
stays the same after a reboot and when a second card is added:

| Endpoint | Form | Example |
| --- | --- | --- |
| onboard PCI card | node, PCI address, PCM id | `node-1-pci-0000-00-1f-3-hdmi-0` |
| USB card with a serial | node, vendor, product, serial, PCM id | `node-1-usb-0573-1573-a34004801402-usb-audio` |
| USB card with a serial, past 63 characters | node, vendor, product, serial hash, PCM id | `studio-screen-12-usb-046d-0a44-5fe3de05-usb-audio` |
| USB card with no serial | node, USB port path, PCM id | `node-1-usb-1-6-usb-audio` |
| Bluetooth speaker | address | `7c-66-ef-01-23-45` |

The PCM id is the driver's name for the endpoint, such as `HDMI 0` or
`USB Audio`, in lowercase with dashes. Every ALSA name starts with the
node name, because a USB serial is not unique across machines: dongles
of one model can report the same serial. The serial tells apart two
identical dongles on one machine. A card that moves to another machine
becomes a new `Sink`, and so does a card with no serial that moves to
another port. The old `Sink` stays with its `spec`, and the operator
on the old machine sets its `Connected` condition to `False`. Delete
it when you no longer need its `spec`. A card that plays and
records through one PCM gives its `Source` the same name with
`-capture` on the end.

A name is a DNS label, so it can have at most 63 characters. A USB
serial is often 20 characters or more, so on a machine with a long
name the serial form can exceed that limit. The name then contains the
first eight hex digits of the serial's SHA-256 in place of the serial,
and keeps its other parts. The hash is stable and differs for each
serial, so two identical dongles on one machine still get two names.
The operator checks each name separately, and `-capture` adds eight
characters. So a dongle whose `Sink` name is 56 to 63 characters keeps
the serial in the `Sink` name and uses the hash in the `Source` name.
To compute the hash by hand:

```sh
printf %s ABCDEF0123456789ABCDEF0 | sha256sum | cut -c1-8
```

The operator refuses any other name that is longer than 63 characters,
and its log names the endpoint and the length.

On an Intel HDMI codec, `hdmi-0` is the card's first HDMI output, not
a physical port. The codec binds a pin to the first free output when a
monitor appears, so on a card with two monitors, the output that each
monitor uses can change between plug events. `status.monitor` shows
which monitor the output feeds now. On a card with one HDMI monitor,
the output does not change.

## How the operator applies the `spec`

The operator writes a declared `volume.level` and `mute` in two cases:
when the declaration changes, and when the endpoint appears, for
example when a speaker reconnects or PipeWire creates the nodes again.
At other times the operator leaves the level alone. When someone
presses a button on the speaker, a client changes the graph, or a
volume request arrives, the level changes, and the operator reports
the new level in `status.observed` without writing the declaration
back.

A declared control and a declared `codec` work differently. The
operator compares each one with the value it last read, and writes to
the hardware only when the two differ. It checks each declared control
against `status.capabilities`. When the card does not declare the
control name, or the value is outside the control's range, the
operator does not write it and logs the reason. An empty `spec` writes
nothing. The operator writes no value that you did not declare, with
one exception: it sets every sink to unity gain when PipeWire creates
the node, so that no software gain below unity lowers the resolution
of the samples before they reach the card. It writes unity only to a node that PipeWire created since
the operator last applied the declaration.

The operator records in `status.applied` the node that it applied the
declaration to, by the node's id and the cookie of the PipeWire that
created it, and the `volume.level` and `mute` that it applied. After a
restart, the operator uses this record to tell which nodes kept
running. A node that `status.applied` names keeps its current level,
because that level can come from a volume request or a press of the
speaker's button, and the declaration does not include those. The
operator writes the declaration to such a node only when it differs
from the declaration in `status.applied`, for example when you changed
it while the operator was down. Every other node is new, so it takes
the declaration, or unity on a sink with no declared level. When the
machine boots, PipeWire starts with a new cookie and creates every
node again, unmuted at its configured level, so each node takes the
declaration on the operator's first pass.

`volume.max` and `volume.step` control the volume requests that the
next section describes, and the operator never writes them to the
hardware. `volume.max` defaults to 100 percent and `volume.step` to 5
percentage points. The API server fills in the defaults when
`spec.volume` is present.

`volume.level`, `mute`, and `controls` apply at once, whether or not a
claim holds the endpoint. `codec` waits until the claim ends, because
a codec switch replaces the speaker's node and interrupts playback.
While a claim holds the speaker, the claim's own `codec` parameter
takes precedence. On a shared speaker, `codec` waits until every claim ends.

`sharedClaims` changes the device that the operator publishes in the
`ResourceSlice`, not the hardware. The operator writes the slice
again, and the new count applies to the next allocation.
[Shared endpoints](/docs/reference/devices/#shared-endpoints) lists
the fields that the slice publishes.

## Volume requests

A remote's volume key reaches a `Sink` through media-operator.
media-operator turns each key press into an absolute level, at most `volume.max`, and
writes the level with the time of the press to
`status.session.volumeAsk`. It writes that block by server-side apply
under its own field manager. This operator writes the rest of `status`
as a merge patch that never names the block, so neither writer removes
the other's fields.

```yaml
status:
  session:
    player: media/den
    volumeAsk:
      level: 45
      mute: false
      at: "2026-10-04T12:15:25.164Z"
```

Each new `at` value is one volume request. The operator applies it
once, with the same write as `volume.level`: the node's gain, or a
Bluetooth speaker's own volume over AVRCP. When several requests arrive before the operator
applies one, it applies only the newest. After the write succeeds, the
operator writes the requested level and mute to `status.observed`
immediately, and the next pass replaces them with what PipeWire
reports. The operator does not apply a request that it finds the first
time it reads the `Sink`, as in its first pass after a start, because
either the previous operator applied that request or the device's
level is newer. It drops a request for an endpoint with no node, and
the endpoint takes `volume.level` when its node appears.

## Observation

The operator updates `status.observed` from two event sources. The
card's control device reports every control write from any process,
every jack change, every monitor change, and a knob turned on a USB
DAC. PipeWire's graph reports every node and device change. So a
change that someone makes with a knob, a remote, or a speaker's own
buttons appears in `observed` on the pass that the event starts. On
that pass the operator writes a declared control or codec back to the
hardware, and does not write a declared level back. The operator also
runs a pass every 60 seconds, for a card whose control events it could
not subscribe to and for a status write that failed.

## The channel layout

The operator declares every sink of a sound card to PipeWire with a
channel layout: the position of each PCM slot, in PipeWire's channel
names. WirePlumber links each channel of a stream to the sink channel
with the same name. It links every stream to a sink with no positions
as two channels, `FL` and `FR`, so a 5.1 or 7.1 stream is mixed down
to them.

The operator reads the layout from the first source in this table that
has one, and `status.layoutSource` names it:

| Source | Where the positions come from |
| --- | --- |
| `Spec` | `spec.layout` |
| `ELD` | the HDMI or DisplayPort monitor's speaker allocation, limited to the largest LPCM channel count that the monitor accepts |
| `ChannelMap` | the channel map that a USB device describes in its descriptors. PipeWire reads it when it opens the device, so `status.layout` is absent |
| `None` | nothing. The analog jack reports no speakers, and neither does an HDMI output whose monitor was off when the pod started |

From the ELD, the operator selects one of the HDMI layouts that
PipeWire's own card profiles use:

| The monitor advertises | `status.layout` |
| --- | --- |
| 8-channel LPCM and `FL/FR`, `LFE`, `FC`, `RL/RR`, `RLC/RRC` | `FL, FR, RL, RR, FC, LFE, SL, SR` |
| 6-channel LPCM and `FL/FR`, `LFE`, `FC`, `RL/RR` | `FL, FR, RL, RR, FC, LFE` |
| anything else | `FL, FR` |

Most televisions accept 8-channel LPCM and have two speakers. Such a
set gets `FL, FR`, so PipeWire mixes the center channel, which
carries the dialog, into the front pair. An HDMI sink keeps its layout
from the ELD while its monitor is off, so a receiver that turns off
and on again changes nothing.

PipeWire reads the layout once, when it starts. When a sink's layout
changes, because `spec.layout` changed or a monitor with a different
layout is connected, the operator writes the new declaration. The
first process of the PipeWire container then restarts PipeWire inside
the container, so the restart never waits in the kubelet's crash
backoff. The
restart ends every stream on the machine, so the operator waits until
no stream is playing. While it waits, `LayoutApplied` is `False` with
the reason `AwaitingIdle`. It stays `False` with the reason
`Restarting` until the new PipeWire starts. Each change posts one
`LayoutChanged` `Event` on the `Sink`, which names the old layout, the
new layout, and the source.

The operator does not write the kernel's channel map. On HDMI the
kernel routes each slot by its standard allocation for the channel
count, so a height position such as `TFL` in `spec.layout` plays from
the speaker that the standard allocation gives that slot. The kernel can route
front heights (CEA allocation 0x2f), but only when a program writes
the PCM's channel map while the device is open and stopped.

## Encoded formats

A receiver plays Dolby Atmos or DTS:X on its height speakers only when
it decodes the track itself. So a sink can send a film's Dolby
or DTS track to its monitor as the encoded bitstream, and
`status.passthrough` lists the formats that it sends this way:

    kubectl get sink <name> -o jsonpath='{.status.passthrough}'

| Name | Format | Also carries |
| --- | --- | --- |
| `AC3` | Dolby Digital | |
| `EAC3` | Dolby Digital Plus | Dolby Atmos |
| `TrueHD` | Dolby TrueHD | Dolby Atmos |
| `DTS` | DTS | |
| `DTS-HD` | DTS-HD High Resolution and Master Audio | DTS:X |

The list contains the formats that the monitor's ELD lists, and the
device attribute `encodedFormats` has the same list for a claim to
select on. The operator declares the sink to PipeWire with these
formats, and with alsa-lib's `hdmi` device for the output in place of
the `hw` device, because PipeWire's ALSA node sends a bitstream only
through a device with that name. An output of a card whose driver is
not `HDA-Intel` sends PCM only, and so does every analog, USB, and
Bluetooth sink.

The sink does not choose what a stream sends. A player whose stream is
a bitstream in one of these formats is linked as it is, and every
other stream plays as PCM. A player has to send only the formats
in the list: PipeWire refuses a bitstream in any other format, and the
stream then plays nothing.
[media-operator](https://liken.sh/media/)
reads this list for a `Player` that sets `passthrough: On`.

While a bitstream plays through a sink:

* The sink's level and mute have no effect, because PipeWire cannot
  scale a bitstream. Only the receiver's own volume changes the
  level.
* An underrun, a moment when the card runs out of samples, breaks the
  bitstream's framing, and the receiver mutes until it finds the
  stream again. With PCM the same underrun is a gap of a few
  milliseconds. PipeWire logs each one, as the
  [install guide](/docs/guides/install/) shows.
* The bitstream has the sink to itself. PipeWire refuses a second
  stream to the sink, and a tap of the sink through `audio-api`, until
  the bitstream ends. When a stream or a tap is already using the
  sink, PipeWire refuses the bitstream instead.

A change of the formats is handled like a layout change. When a monitor
reports a different list, the operator writes the declaration again,
and PipeWire restarts when no stream is playing. `LayoutApplied`
reports the wait, and the `LayoutChanged` `Event` names the formats. A
sink keeps its formats while its monitor is off.

## Events

The operator posts a Kubernetes `Event` on a `Sink` for each change of
a condition and for each action it takes. A `Sink` is cluster-scoped,
so its `Event`s are in the `default` namespace. `kubectl describe
sink` shows them. `kubectl events --for` shows them only with
`-n default` or `-A`:

    kubectl events -n default --for sink/<name>

The API server deletes an `Event` one hour after its last write, so
the conditions and the operator's log keep the facts for longer. When
an `Event` repeats within 10 minutes, the operator patches the count
of the existing `Event` in place of posting a new one.

Each condition change posts one `Event` with the condition's own
reason and message, such as `NoMonitor`, `JackEmpty`, or
`AwaitingIdle`. The first status write posts one for each condition. A
change of `Ready` to `False` is a `Warning` while `Connected` is
`True`, because the endpoint is connected but PipeWire has no node to
send sound through. Every other condition change is `Normal`: a
television that turns off and a plug pulled from a jack are ordinary
actions by a person.

The operator posts these reasons for the actions and faults that
change no condition:

| Reason | Type | When |
|---|---|---|
| `LayoutChanged` | `Normal` | The operator wrote a new channel layout, and PipeWire restarts in its container to apply it. |
| `LayoutWriteFailed` | `Warning` | The operator could not write the declaration that has the new layout. The sink keeps its layout, and each pass tries the write again. |
| `SpecRefused` | `Warning` | The `spec` sets a value that the endpoint does not accept, such as a codec that the speaker does not offer. The message names each refused value. |
| `PipeWireLost` | `Warning` | A read of PipeWire's graph failed. After 3 failed reads in a row, the operator taints every output and restarts. |
| `PipeWireRecovered` | `Normal` | A read of PipeWire's graph succeeded again, after one or more failed reads. |
| `BluetoothUnavailable` | `Warning` | On a speaker's `Sink`: a read of the paired speakers from `bluetoothd` failed. The speaker is published with a taint until a read succeeds. |
| `BluetoothAvailable` | `Normal` | On a speaker's `Sink`: a read from `bluetoothd` succeeds again. |
| `Captured` | `Normal` | The capture API returned the audio of the `Sink` to a caller. The message names the caller and the format. |

The operator posts an `Event` for a fault when it first meets the
fault, and does not post it again on each later pass that meets the
same fault.
