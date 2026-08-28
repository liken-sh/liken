## The name

The name is built from the hardware's own identity, so it survives
a reboot and a second card:

| Endpoint | Form | Example |
| --- | --- | --- |
| onboard PCI card | node, PCI address, PCM id | `node-1-pci-0000-00-1f-3-hdmi-0` |
| USB card with a serial | vendor, product, serial, PCM id | `usb-0573-1573-a34004801402-usb-audio` |
| USB card with no serial | node, USB port path, PCM id | `node-1-usb-1-6-usb-audio` |
| Bluetooth speaker | address | `7c-66-ef-01-23-45` |

The PCM id is the driver's name for the endpoint, `HDMI 0` or
`USB Audio`, lowercased with dashes. A USB card with a serial keeps
its `Sink` when it moves to another machine, and `status.node` says
where it is. A card with no serial that moves to another port
becomes a new `Sink`. A card that plays and records through one PCM
gives its `Source` the same name with `-capture` on the end.

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
resolution before the codec runs.

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
