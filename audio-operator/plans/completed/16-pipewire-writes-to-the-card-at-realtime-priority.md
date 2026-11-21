# 16, PipeWire writes to the card at realtime priority

Plan 16. Proposed and built on 2026-11-21. The drill on a receiver has
not run, and the end of this plan says what it must show.

A sink that plays a bitstream drops out for a moment several times in
each film. Each dropout is an underrun of the card: PipeWire's thread
that writes to the card ran late, and the card ran out of samples. This
plan runs that thread at realtime priority, and keeps a log line for
each underrun on every machine.

## What was measured

On 2026-11-21, on a home cluster, a `Player` with `passthrough: On`
played through an A/V receiver on the HDMI output of an HDA-Intel card.
The audio-operator image was `2026.11.19-002`, with PipeWire 1.6.9.
PipeWire's log level was raised to 3 by hand with `pw-metadata`, and
`pw-top -b` recorded the sink's `ERR` column.

| Track | What PipeWire opened | Play time | Underruns | Dropouts heard |
|---|---|---|---|---|
| E-AC-3 5.1 with Atmos | IEC958 at 192 kHz, quantum 4096 | about 50 minutes | 6 | each one that was reported |
| DTS 5.1 | IEC958 at 48 kHz, quantum 1024 | about 3 minutes | 0 | none |

The person who watched reported each dropout in a chat as it happened.
Each report matched a line such as this one, from `pw.node` at the info
level:

```
XRun! rate:4096/192000 count:3 time:163089016944 delay:16 max:53 (0 suppressed)
```

The underruns came during steady playback and also seconds after a
resume. They did not come on every resume. mpv's own node had no
error, so the late node was PipeWire's driver, the sink.

The film's E-AC-3 track was checked for a fault in the file.
`ffmpeg -err_detect crccheck+bitstream+buffer` decoded the whole track
with no error, and every packet was 2560 bytes and 32 ms long, with no
gap in the timestamps.

The `pipewire` container ran with every capability dropped, and the
image did not contain `libpipewire-module-rt.so`. PipeWire's packaged
`pipewire.conf` loads that module with `ifexists nofail`, so PipeWire
skipped it with no message and ran every thread at normal priority.
The container's CPU request is 50m, so under load its thread waits
behind every other thread on the node.

With PCM an underrun is a gap of one quantum, about 21 ms here, and a
person rarely hears it. With a bitstream the gap breaks the IEC 61937
framing, and the receiver mutes until it finds the stream again. So
the underruns can be older than passthrough, and passthrough made them
audible.

## The design

* The closure copies `libpipewire-module-rt.so`, and the smoke test
  checks for it by name, because PipeWire skips it in silence when it
  is absent.
* The `pipewire` container adds the `SYS_NICE` capability. The other
  containers stay as they are.
* The packaged arguments of the module do not change: `rt.prio = 88`
  and `nice.level = -11`. With `SYS_NICE`, the module sets the thread
  `data-loop.0` to `SCHED_FIFO` directly, and it does not try RTKit
  or the portal.
* The smoke test runs PipeWire with every capability dropped and
  `SYS_NICE` added, as the pod does, and fails unless `data-loop.0`
  runs at `SCHED_FIFO`.
* `config/50-pipewire-log.conf` sets `log.level = "2,pw.node:3"`. So
  PipeWire logs each underrun and each change of a node's state, and
  every other topic at the warning level.

The liken kernel is built without `CONFIG_RT_GROUP_SCHED`, so a
container's cgroup has no realtime budget of its own to configure.
`liken-system` has no Pod Security label that refuses the capability.

On the laptop with PipeWire in the image built from this tree, the
thread ran at `FF 88` with `SYS_NICE`. Without the capability, the
module logged `Realtime scheduling disabled: insufficient realtime
privileges` at the warning level, and the thread ran at `TS`. A playback
of E-AC-3 at 192 kHz, with one underrun forced by stopping PipeWire for
300 ms, wrote 19 lines at the info level, all from `pw.node`. At level
3 for every topic, the cluster wrote 55 to 70 lines for each pause and
resume, most of them format negotiation from `pw.link`.

## Considered and set aside

* **Realtime for mpv.** mpv's client library loads the same module,
  and the player container would need `SYS_NICE`. That gives the
  capability to every playback pod, and mpv decodes whatever file it
  is given. mpv's node was never the late one in the measurements.
* **A larger quantum or `api.alsa.headroom`.** Either one gives the
  thread more time. Neither one keeps the thread from waiting behind
  other work, and both add latency to every stream on the sink.
* **A metric of underruns.** PipeWire does not report an underrun count
  in `pw-dump`. The profiler reports it, but it builds a record on the
  realtime thread for each cycle while a client listens. Parsing the
  log line in the `pipewire` container depends on the text of a log
  message. The maintainer deferred the metric.
* **A PipeWire that fails to start without realtime.** The packaged
  entry has `nofail`, and a drop-in can override the module's
  arguments but not its flags. The smoke test and the module's warning
  cover the failure.

## Not proved yet

On the home cluster that measured the dropouts:

* An E-AC-3 film at 192 kHz plays for at least 30 minutes with no
  `XRun!` line and no dropout heard, with pauses and resumes.
* `data-loop.0` of the running PipeWire is at `SCHED_FIFO`.
