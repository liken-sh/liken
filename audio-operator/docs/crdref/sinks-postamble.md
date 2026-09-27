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

## The resting layer

A declared field is a standing instruction. The operator compares
the declaration with the value it last read, and it writes the
hardware only where the two diverge. A declared control is
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

An idle node reports no level, and PipeWire announces no write to
one, so the operator cannot read whether an idle node already holds
its declared `volume` and `mute`. After a restart, the operator
treats the declaration as the level an idle node holds and writes
nothing to it. It compares the declaration with the node's level
when the node runs, and it writes a changed declaration at once.

`volume`, `mute`, and `controls` apply at once, whether a claim
holds the endpoint or not. `codec` waits for the claim to end,
because a codec switch replaces the speaker's node and interrupts
playback, and a claim's own `codec` parameter wins while it holds
the speaker.

## Observation

`status.observed` follows two event sources and no timer. The
card's control device reports every control write from any process,
every jack change, every monitor change, and a knob turned on a USB
DAC. PipeWire's graph reports every node and device change. So a
change a person made with a knob, a remote, or a speaker's own
buttons shows in `observed` within about a second. A value the spec
declares is written back on the same event.
