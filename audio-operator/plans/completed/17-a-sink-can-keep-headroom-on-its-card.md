# 17, A sink can keep headroom on its card

Plan 17. Proposed and built on 2026-11-21. It follows
[plan 16](16-pipewire-writes-to-the-card-at-realtime-priority.md),
which did not stop the dropouts. The drill on the receiver that
dropped out has not run with this build, and the end of this plan
says what it must show.

`spec.headroom.samples` on a `Sink` makes PipeWire keep that many
samples queued on the card beyond the one quantum that it writes
ahead. A sink whose card underruns without it can then play a
bitstream with no dropout.

## What was measured

On 2026-11-21, on a home cluster, the HDMI sink of an HDA-Intel card
played E-AC-3 as a bitstream at 192 kHz to an A/V receiver, with a
quantum of 4096 samples. Each dropout that the person watching
reported matched an `XRun!` line of that sink in PipeWire's log.

| PipeWire | Headroom | Play time | Underruns |
|---|---|---|---|
| Normal priority, before plan 16 | 0 | about 50 minutes | 6 |
| `SCHED_FIFO` priority 88, plan 16 | 0 | about 12 minutes | 1 |
| `SCHED_FIFO` priority 88 | 4096, set with `pw-cli set-param` | about 26 minutes | 0 |

A debug pod on the machine read `/proc` and showed `data-loop.0` at
policy 1, `SCHED_FIFO`, and priority 88. So the underrun under plan 16
did not come from a thread that started late. The run with headroom
is short. At the earlier rate of about one underrun every 8 to 12
minutes, 26 minutes with none would occur by chance about 7% of the
time.

The measurements do not show why the position estimate is off. The
cause can be the position that the HDA driver reports, a drift between
the card's clock and the system clock at 192 kHz, or both. No one
counted the underruns of PCM on that sink before passthrough, because
PipeWire did not log them, so the measurements also do not show
whether PCM underruns too and a person does not hear it.

## The design

* `spec.headroom` on a `Sink` is an object, so that a later property
  can state the headroom as a duration. Its one property today is
  `samples`, an integer from 0 to 65536, which is required when
  `headroom` is present.
* When `spec.headroom` is absent, the operator declares the sink as
  before, and PipeWire keeps no headroom. The operator sets no default
  for every sink, because every stream on a sink with headroom plays
  later by the headroom, and a sink for recording or live sound needs
  the lowest latency it can get.
* The operator declares the node with `api.alsa.headroom`. The value
  is part of the layout, as the encoded formats are, so a change uses
  the machinery of [plan 12](12-channel-layouts.md): one comparison
  finds it, the declaration is written again when no stream plays,
  `LayoutApplied` reports the wait, and one `LayoutChanged` `Event`
  names the old and new values.
* The value is in samples at the rate the sink runs, which is
  PipeWire's unit. 4096 samples is 21 ms at 192 kHz and 85 ms at
  48 kHz.
* A Bluetooth speaker ignores the field, as it ignores `spec.layout`.

## Considered and set aside

* **A duration.** It states the same delay at every rate. PipeWire
  reads the headroom when it opens the card, which is also when a new
  stream sets the rate. So the operator would always set the value one
  open late. A WirePlumber hook in the linking chain could set it
  before the open, from a value in PipeWire's metadata. That design
  was not tested, and the field leaves room for it as
  `spec.headroom.duration`.
* **`pw-cli set-param` on the running node.** It needs no restart, but
  the operator would have to set the value again each time PipeWire
  builds the node, and the declaration would not show it.
* **Headroom on every sink, or on every sink that passes
  bitstreams.** The first adds latency for every card. The second
  assumes that only the bitstream path underruns, which nothing has
  shown.
* **A setting for each machine or the cluster.** The headroom that a
  card needs depends on the card, and each sink is one PCM device.

## Not proved yet

On the sink that dropped out, with `spec.headroom.samples` of 4096
from this build:

* The declaration has `api.alsa.headroom` of 4096, and `LayoutApplied`
  names it.
* An E-AC-3 film at 192 kHz plays for at least 30 minutes with pauses
  and resumes, with no `XRun!` line and no dropout heard.
* The picture and the sound stay in step.
