# Prometheus metrics

Plan 26. Proposed.

The contract for every metric here, the three layers, the names, the
port table, and the monitoring component, is [liken milestone
65](https://github.com/liken-sh/liken/blob/main/plans/65-prometheus-metrics.md).
This plan states only what this operator adds.

## The problem

A film that does not start, a software decode that pins a small box at
1300 millicores, and a bus that dropped its connection are the three
failures a room has. All three are visible only by reading logs today.

## The design

Layers 1 and 2 under the `media_` prefix. Three processes serve metrics,
each on port 9200: the operator, the command sidecar in every Play pod,
and the idle screen. None of the three shares a pod with another, so
the one port never collides. The sidecar is the operator's own binary
in another role, and it is the one process that holds mpv's IPC
socket, so mpv's facts are its to report.
The idle screen is Rust and uses the `metrics` facade with the
Prometheus exporter, the same pair Corrosion uses. Neither the sidecar
nor the idle screen has a layer 2.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| media-operator | `media_players{zone, state}` | gauge | idle, playing, paused |
| media-operator | `media_playback_starts_total` | counter | plays over time |
| media-operator | `media_playback_failures_total{reason}` | counter | the film did not start |
| media-operator, idle-screen | `media_bus_connected` | gauge | the MQTT bus is up |
| command sidecar | `media_decode_info{codec, hardware}` | gauge, info | a software decode is a 1300 millicore line |
| command sidecar | `media_dropped_frames_total` | counter | stutter |
| command sidecar | `media_av_delay_seconds` | gauge | sync drift |

The last three read mpv's own properties: `hwdec-current` and
`video-codec` for the decode, `frame-drop-count` for dropped frames, and
`avsync` for the delay. If media-screen cannot observe one of these
through the client API it already uses, the plan says so and the metric
waits.

The monitoring component at `deploy/monitoring/` holds a PodMonitor for
the operator and one for each screen pod.

## Proof

Failing tests first. On liken-1, play a film with hardware decode and
one without, and read the two decode series; pull the bus and read
`media_bus_connected` fall.