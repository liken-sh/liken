---
title: Metrics
weight: 70
toc: true
---

# Metrics

The operator, the command sidecar in each `Play` pod, the idle screen,
and `media-api` each serve Prometheus metrics on port 9200.

The base in `deploy/` works without Prometheus. If you run
prometheus-operator, add the `deploy/monitoring` `Component` beside the
base. It creates the `PodMonitor`s that scrape these pods. The
`PodMonitor`s are in `liken-system`, and the ones for the idle screen
and the playback pods select pods in every namespace, because a
`Player` and a `Play` can be in any namespace.

```yaml
resources:
  - https://github.com/liken-sh/liken//media-operator/deploy?ref=<tag>
components:
  - https://github.com/liken-sh/liken//media-operator/deploy/monitoring?ref=<tag>
```

Every process serves `liken_build_info{component, version}`, a gauge
that is always 1 and gives the component and the version that the
process runs. The operator, the command sidecar, and `media-api` also
serve the standard `go_*` and `process_*` metrics.

| Component | Metric | Type | What it measures |
| --- | --- | --- | --- |
| media-operator | `media_reconcile_duration_seconds{kind}` | histogram | Time of one reconcile of one `kind` |
| media-operator | `media_reconcile_errors_total{kind}` | counter | Reconciles that returned an error |
| media-operator | `media_watch_restarts_total{kind}` | counter | Times a watch stream ended and the operator opened it again |
| media-operator | `media_players{zone, state}` | gauge | `Player`s in each `state`: idle, playing, or paused |
| media-operator | `media_playback_starts_total` | counter | `Play`s that reached the `Running` phase |
| media-operator | `media_playback_failures_total{reason}` | counter | `Play`s that reached the `Failed` phase, by `reason` |
| media-operator | `media_display_restarts_total{player}` | counter | Times the kubelet restarted a playback pod's display container, by `player` |
| media-operator, idle-screen | `media_bus_connected` | gauge | Whether the process has a live MQTT session |
| command sidecar | `media_decode_info{codec, hardware}` | gauge | The codec of the current item, and whether the GPU decodes it. Software decoding uses much more CPU |
| command sidecar | `media_dropped_frames_total` | counter | Frames mpv dropped |
| command sidecar | `media_av_delay_seconds` | gauge | mpv's audio-to-video offset. Positive when audio is ahead of video |
| media-api | `media_api_requests_total{route, method, status}` | counter | Requests, by route, method, and status |
| media-api | `media_api_request_seconds{route}` | histogram | Time to the first response header |
| media-api | `media_api_streams_active{aspect}` | gauge | Streams that `media-api` is sending now, by `aspect` |
| media-api | `media_api_upstream_requests_total{upstream, status}` | counter | Requests that `media-api` made to `display-api` and `audio-api`, by `upstream` and status |
| media-api | `media_api_compose_offset_seconds` | histogram | In a composed capture, how far apart the response headers of `display-api` and `audio-api` arrived, without a sign |
| media-api | `media_api_compose_offset_last_seconds` | gauge | The same offset for the last composed capture, with a sign. Negative when the audio headers arrived first |
| media-api | `media_api_certificate_expiry_seconds` | gauge | Seconds until the serving certificate expires |
