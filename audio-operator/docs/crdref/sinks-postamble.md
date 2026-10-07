## The name

The name is built from the hardware's own identity, so it survives
a reboot and a second card:

| Endpoint | Form | Example |
| --- | --- | --- |
| onboard PCI card | node, PCI address, PCM id | `node-1-pci-0000-00-1f-3-hdmi-0` |
| USB card with a serial | node, vendor, product, serial, PCM id | `node-1-usb-0573-1573-a34004801402-usb-audio` |
| USB card with a serial, past 63 characters | node, vendor, product, serial hash, PCM id | `studio-screen-12-usb-046d-0a44-5fe3de05-usb-audio` |
| USB card with no serial | node, USB port path, PCM id | `node-1-usb-1-6-usb-audio` |
| Bluetooth speaker | address | `7c-66-ef-01-23-45` |

The PCM id is the driver's name for the endpoint, `HDMI 0` or
`USB Audio`, lowercased with dashes. Every ALSA form starts with the
node, because a USB serial is not unique across machines: dongles of
one model can all report the same serial. The serial tells apart two
identical dongles on one machine. A card that moves to another
machine becomes a new `Sink`, and so does a card with no serial that
moves to another port. The old `Sink` stays, with its `spec`, and
the operator on the old machine sets its `Connected` condition to
`False`. Delete it when you no longer need its declaration. A card that plays and records through one PCM
gives its `Source` the same name with `-capture` on the end.

A name is a DNS label, so it holds at most 63 characters. A USB
serial is often 20 characters or more, so on a machine with a longer
name the serial form can pass that. Then the name holds the first
eight hex digits of the serial's SHA-256 in place of the serial, and
keeps the rest. The hash is stable and differs for each serial, so
two identical dongles on one machine still get two names. The
operator tests each name alone, and `-capture` adds eight
characters. So a dongle whose `Sink` name is 56 to 63 characters
keeps the serial in the `Sink` name and holds the hash in the
`Source` name. To compute the hash by hand:

```sh
printf %s ABCDEF0123456789ABCDEF0 | sha256sum | cut -c1-8
```

The operator refuses any other name that passes 63 characters, and
its log names the endpoint and the length.

On an Intel HDMI codec, `hdmi-0` names the card's first HDMI slot
and not a physical port. A pin binds to the first free slot when a
monitor appears, so on a card with two monitors the slot each one
lands in can change between plug events. `status.monitor` reports
which monitor the slot feeds now, and a machine with one HDMI
monitor never sees the difference.

## How the operator applies the `spec`

The operator writes a declared `volume.level` and `mute` when the
declaration changes and when the endpoint appears: a speaker that
reconnects, or a node that PipeWire builds again. At every other
time it follows the device. A press of a speaker's own button, a
client that changes the graph, and a volume ask move the level, and
the operator reports the new level in `status.observed` and does not
write the declaration back. A declared control and a declared `codec`
are standing instructions: the operator compares each one with the
value it last read, and writes the hardware only where the two
diverge. A declared control is
validated against `status.capabilities`: a name the card does not
declare, or a value out of its range, fails the pass with the reason
in the operator's log and is never written. An empty `spec` writes
nothing at all. The operator invents no value: an endpoint with no
declarations keeps whatever the hardware holds, except that every
sink starts at unity gain so that no hidden multiplier costs
resolution before the codec runs. The unity write goes only to a
node PipeWire builds while the operator runs. After a restart, the
operator writes nothing to a sink that was there before it started,
and the sink keeps the level it holds.

A restart writes no declared level either. The level an endpoint
holds when the operator starts can be a volume ask or a press of the
speaker's button that the declaration does not know, and an idle
node reports no level, so the operator cannot read whether it
already holds the declaration. The first pass treats the declaration
as the level each endpoint holds, and a later change of the
declaration is written at once.

`volume.max` and `volume.step` bound the volume asks below, and the
operator writes neither to the hardware. Each one has a default, 100
and 5 percent, which the API server fills in when `spec.volume` is
present.

`volume.level`, `mute`, and `controls` apply at once, whether a claim
holds the endpoint or not. `codec` waits for the claim to end,
because a codec switch replaces the speaker's node and interrupts
playback, and a claim's own `codec` parameter wins while it holds
the speaker.

## The volume asks

A remote's volume key reaches a `Sink` through the media operator.
It turns each press into an absolute level, at most `volume.max`,
and writes it into `status.session.volumeAsk` with the time of the
press. It writes that block by server-side apply under its own field
manager, and this operator writes the rest of `status` as a merge
patch that never names it, so neither writer removes the other's
fields.

```yaml
status:
  session:
    player: media/den
    volumeAsk:
      level: 45
      mute: false
      at: "2026-10-04T12:15:25.164Z"
```

Each new `at` is one ask, and the operator applies it once, with the
same write that `volume.level` takes: the node's gain, or a
Bluetooth speaker's own volume over AVRCP. The ask does not wait for
the 1.5 second settle window that gathers a burst of hardware events
into one `ResourceSlice` write, because a level write changes no
`ResourceSlice`. When several asks arrive before the operator applies
one, it applies only the newest. When the write lands, the operator
writes the asked level and mute into `status.observed` at once, and
the next pass replaces them with what PipeWire reports. It applies no ask that it finds the
first time it reads the `Sink`, as in its first pass after a start,
because the last operator applied that ask, or the device's level is
newer than it. An ask for an endpoint with no node is dropped, and
the endpoint takes `volume.level` when it appears.

## Observation

`status.observed` follows two event sources and no timer. The
card's control device reports every control write from any process,
every jack change, every monitor change, and a knob turned on a USB
DAC. PipeWire's graph reports every node and device change. So a
change a person made with a knob, a remote, or a speaker's own
buttons shows in `observed` within about a second. A declared
control or codec is written back on the same event, and a declared
level is not.

## The channel layout

The operator declares every sink of a sound card to PipeWire with a
channel layout: the position of each PCM slot, in PipeWire's channel
names. WirePlumber links each channel of a stream to the sink's
channel of the same name. It links every stream to a sink with no
positions as two channels, `FL` and `FR`, so a 5.1 or 7.1 stream is
mixed down into them.

The operator takes the layout from the first source that gives one,
and `status.layoutSource` names it:

| Source | Where the positions come from |
| --- | --- |
| `Spec` | `spec.layout` |
| `ELD` | the HDMI or DisplayPort monitor's speaker allocation, capped by the largest LPCM channel count it accepts |
| `ChannelMap` | the channel map a USB device describes in its descriptors. PipeWire reads it when it opens the device, so `status.layout` is absent |
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
carries the dialog, into the front pair. An HDMI sink keeps its
layout from the ELD while its monitor is off, so a receiver that
turns off and on again changes nothing.

PipeWire reads the layout once, when it starts. When a sink's layout
changes, because `spec.layout` changed or a monitor with another
layout answers, the operator writes the new declaration and the
kubelet restarts the PipeWire container. The restart ends every
stream on the machine, so the operator waits until no stream plays.
`LayoutApplied` is `False` with the reason `AwaitingIdle` while it
waits, and with the reason `Restarting` until the new PipeWire
starts. Each change posts one `LayoutChanged` `Event` on the `Sink`,
which names the old layout, the new one, and the source.

Two things the operator does not do:

* It does not write the kernel's channel map. On HDMI the kernel
  routes each slot by its standard allocation for the channel count,
  so a height position such as `TFL` in `spec.layout` names a slot
  that the kernel sends to another speaker. The kernel can route
  front heights (CEA allocation 0x2f), but only when a program
  writes the PCM's channel map while the device is open and stopped.
* It does not pass a bitstream through. A receiver that plays height
  speakers from Dolby Atmos or DTS:X needs the compressed stream,
  and the operator sends PCM. Passthrough is a separate design.

## Events

The operator posts a Kubernetes `Event` on a `Sink` for each change
of a condition and for each action it takes. A `Sink` is
cluster-scoped, so its `Event`s are in the `default` namespace.
`kubectl describe sink` shows them. `kubectl events --for` shows them
only with `-n default` or `-A`:

    kubectl events -n default --for sink/<name>

The API server deletes an `Event` one hour after its last write, so
the conditions and the operator's log hold the facts for longer. The
operator patches the count of an `Event` that repeats within 10
minutes, in place of a new `Event`.

Each condition change posts one `Event` with the condition's own
reason and message, such as `NoMonitor`, `JackEmpty`, or
`AwaitingIdle`. The first status write posts one for each condition.
A change of `Ready` to `False` is a `Warning` while `Connected` is
`True`, because sound can leave the endpoint and PipeWire holds no
node to send it through. Every other condition change is `Normal`: a
television that turns off and a plug pulled from a jack are things a
person does.

The operator posts these reasons for the actions and faults that
change no condition:

| Reason | Type | When |
|---|---|---|
| `LayoutChanged` | `Normal` | The operator wrote a new channel layout, and the kubelet restarts the PipeWire container to apply it. |
| `LayoutWriteFailed` | `Warning` | The operator could not write the declaration that holds a new layout. The sink keeps its layout, and each pass tries the write again. |
| `SpecRefused` | `Warning` | The `spec` states a value the endpoint does not take, such as a codec the speaker does not offer. The message names each refused value. |
| `PipeWireLost` | `Warning` | A read of PipeWire's graph failed. After 3 failed reads in a row, the operator taints every output and restarts. |
| `PipeWireRecovered` | `Normal` | PipeWire answers a graph read again, after one or more that failed. |
| `BluetoothUnavailable` | `Warning` | On a speaker's `Sink`: `bluetoothd` did not answer a read of the paired speakers. The speaker publishes with a taint until it answers. |
| `BluetoothAvailable` | `Normal` | On a speaker's `Sink`: `bluetoothd` answers again. |
| `Captured` | `Normal` | The capture API returned the audio of the `Sink` to a caller. The message names the caller and the format. |

A fault posts once when the operator first meets it, not once for
each pass that meets it again.
