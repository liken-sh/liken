---
title: Metrics
weight: 40
toc: true
---

# Metrics

Three processes serve Prometheus metrics at `/metrics`, under `liken`'s [shared
contract](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md).
The `display-operator` container serves plain HTTP on port 9200, one
per node. The `display-capture` container serves them on port 9201,
the same TLS port as the captures, and the route needs no token. The
`display-api` `Deployment` serves plain HTTP on port 9200, once for
the cluster. The base applies with no Prometheus in the cluster. An
owner who runs the prometheus-operator adds the `deploy/monitoring`
component beside the base, which holds a `PodMonitor` for each port.

Each process also sets `liken_build_info{component, version}` to 1.
The `component` is `display-operator`, `display-capture`, or
`display-api`.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| display-operator | `display_output_connected{output}` | gauge | a monitor that went away |
| display-operator | `display_output_claimed{output}` | gauge | the hardware triple |
| display-operator | `display_observation_valid{source}`, `display_observation_last_success_timestamp_seconds{source}` | gauge | the card or the compositor socket stopped answering |
| display-operator | `display_output_mode_info{output, mode, refresh}` | gauge, info | what is actuated on each panel |
| display-operator | `display_compositor_restarts_total{reason}` | counter | the restarts the operator orders, by reason; the unbounded-restart problem as a rate |
| display-operator | `display_compositor_serving` | gauge | the compositor answers the handshake, refuses the connect, or answers nothing |
| display-operator | `display_compositor_container_restarts_total` | counter | the kubelet's own count, which includes the restarts nobody ordered |
| display-operator | `display_surfaces{output}` | gauge | what the compositor holds; a stuck surface |
| display-operator | `display_panel_power{output}`, `display_panel_brightness{output}` | gauge | the DDC state the idle screen drives |
| display-operator | `display_reconcile_duration_seconds{kind}` | histogram | how long one pass of the reconcile loop took |
| display-operator | `display_reconcile_errors_total{kind}` | counter | passes of the reconcile loop that returned an error |
| display-operator | `display_watch_restarts_total{kind}` | counter | watches the API server accepted after the first one, for each kind |
| display-capture | `display_capture_ready` | gauge | the sidecar holds the certificate the API verifies it under |
| display-capture | `display_capture_conversion{graph}` | gauge | which of the two frame-conversion graphs the node uses, `vaapi` or `software` |
| display-capture | `display_captures_active{aspect}` | gauge | captures running on the node now |
| display-capture | `display_capture_bytes_total{aspect,format}`, `display_capture_seconds_total{aspect,format}` | counter | how much the sidecar encoded and sent, and how long it streamed |
| display-capture | `display_capture_frames_total{format}` | counter | frames taken from the compositor |
| display-capture | `display_capture_failures_total{reason}` | counter | captures that failed |
| display-api | `display_api_requests_total{route,method,status}` | counter | requests answered |
| display-api | `display_api_request_seconds{route}` | histogram | time to the response headers |
| display-api | `display_api_streams_active{aspect}` | gauge | capture responses the API is streaming now |
| display-api | `display_api_certificate_expiry_seconds` | gauge | when the serving certificate expires, in seconds since the epoch |

```yaml
resources:
  - https://github.com/liken-sh/liken//display-operator/deploy?ref=<version>
components:
  - https://github.com/liken-sh/liken//display-operator/deploy/monitoring?ref=<version>
```

## The card observation

`display_observation_valid{source="card"}` reports whether the last
read of the card succeeded. The operator reads the card only while it
holds a connection to a compositor. While it holds none, the operator
records no card observation: the gauge keeps the value of the last
read, and `display_observation_last_success_timestamp_seconds{source="card"}`
keeps the time of the last successful read, so its age grows.

## Compositor restart reasons

`display_compositor_restarts_total` counts each restart of the
compositor that the operator orders. The `reason` label says why:

| `reason` | Why the operator restarts the compositor |
| --- | --- |
| `mode` | A claim's `mode` parameter or a `Display`'s `spec.mode` changes the mode that a screen runs. Weston reads its config only at startup. |
| `heal` | The compositor re-created an output with another monitor or another mode, so the canvases on the other screens have the wrong size. |
| `hung` | The compositor accepted connections on its socket and answered nothing for 10 seconds. The operator sends `SIGKILL`. |
| `masterless` | A read of the card found that the compositor holds no DRM master, so it cannot show a frame. The operator restarts each compositor process for this reason at most once. |

A compositor that exits on its own has no reason, and this counter
does not count it. `display_compositor_container_restarts_total`
counts it, from the kubelet's restart count.
