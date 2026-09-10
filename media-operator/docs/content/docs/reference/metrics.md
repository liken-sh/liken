---
title: Metrics
weight: 70
toc: true
---

# Metrics

The operator, the command sidecar in every Play pod, and the idle
screen each serve Prometheus metrics on `:9200`, under
[milestone 65](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md)'s
shared contract: every process on the cluster network uses the same
port, since no two of these pods ever share one. The base in `deploy/`
needs no Prometheus: every
listener answers with nothing scraping it. An owner who runs the
prometheus-operator adds `deploy/monitoring`, the `Component` that
carries a `PodMonitor` for each of these pods, beside the base.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| media-operator | `media_players{zone, state}` | gauge | idle, playing, paused |
| media-operator | `media_playback_starts_total` | counter | plays over time |
| media-operator | `media_playback_failures_total{reason}` | counter | the film did not start |
| media-operator, idle-screen | `media_bus_connected` | gauge | the MQTT bus is up |
| command sidecar | `media_decode_info{codec, hardware}` | gauge, info | a software decode is a 1300 millicore line |
| command sidecar | `media_dropped_frames_total` | counter | stutter |
| command sidecar | `media_av_delay_seconds` | gauge | sync drift |

```yaml
resources:
  - https://github.com/liken-sh/media-operator//deploy?ref=<tag>
components:
  - https://github.com/liken-sh/media-operator//deploy/monitoring?ref=<tag>
```
