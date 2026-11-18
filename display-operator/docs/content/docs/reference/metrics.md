---
title: Metrics
weight: 40
toc: true
---

# Metrics

Three processes serve Prometheus metrics at `/metrics`, following
`liken`'s [shared
contract](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md).
The `display-operator` container runs on each node and serves plain
HTTP on port 9200. The `display-capture` container serves metrics on
port 9201, the TLS port that it also uses for captures, and the
metrics route needs no token. The `display-api` `Deployment` runs
once for the cluster and serves plain HTTP on port 9200.

The base in `deploy` needs no Prometheus. If you run the
prometheus-operator, add the `deploy/monitoring` component beside the
base. It has a `PodMonitor` for each port:

```yaml
resources:
  - https://github.com/liken-sh/liken//display-operator/deploy?ref=<version>
components:
  - https://github.com/liken-sh/liken//display-operator/deploy/monitoring?ref=<version>
```

Each process also sets `liken_build_info{component, version}` to 1.
The `component` label is `display-operator`, `display-capture`, or
`display-api`.

| Component | Metric | Type | What it shows |
| --- | --- | --- | --- |
| display-operator | `display_output_connected{output}` | gauge | 1 while a monitor is connected to the output, 0 while none is |
| display-operator | `display_output_claimed{output}` | gauge | 1 while a prepared claim holds the output, 0 while none does |
| display-operator | `display_observation_valid{source}`, `display_observation_last_success_timestamp_seconds{source}` | gauge | whether the last read of the `card` or the `compositor` socket succeeded, and when a read last succeeded. See [the card observation](#the-card-observation) |
| display-operator | `display_output_mode_info{output, mode, refresh}` | gauge, info | the mode and refresh rate that each output runs. The value is always 1 |
| display-operator | `display_compositor_restarts_total{reason}` | counter | how many times the operator ended the compositor, by reason. See [compositor restart reasons](#compositor-restart-reasons) |
| display-operator | `display_compositor_serving` | gauge | 1 while the compositor replies on its Wayland socket, 0 while the socket refuses the connection or the compositor does not reply |
| display-operator | `display_compositor_container_restarts_total` | counter | how many times the kubelet restarted the compositor's container after the compositor exited without an order from the operator |
| display-operator | `display_surfaces{output}` | gauge | how many surfaces (windows) the compositor has on the output |
| display-operator | `display_panel_power{output}`, `display_panel_brightness{output}` | gauge | the last DDC/CI reading of the panel: power is 1 for on and 0 for off or standby, and brightness is in the panel's own units |
| display-operator | `display_reconcile_duration_seconds{kind}` | histogram | how long one pass of the reconcile loop took |
| display-operator | `display_reconcile_errors_total{kind}` | counter | how many passes of the reconcile loop returned an error |
| display-operator | `display_watch_restarts_total{kind}` | counter | how many times the operator opened a watch again after the first one, for each kind |
| display-capture | `display_capture_ready` | gauge | 1 while the container has the certificate that `display-api` verifies it by, 0 while it does not |
| display-capture | `display_capture_conversion{graph}` | gauge | 1 on the frame conversion that the node uses, `vaapi` or `software`, and 0 on the other |
| display-capture | `display_captures_active{aspect}` | gauge | how many captures are running on the node now |
| display-capture | `display_capture_bytes_total{aspect,format}`, `display_capture_seconds_total{aspect,format}` | counter | how many bytes the container encoded and sent, and how many seconds it streamed |
| display-capture | `display_capture_frames_total{format}` | counter | how many frames the container received from the compositor |
| display-capture | `display_capture_failures_total{reason}` | counter | how many captures failed, by reason |
| display-api | `display_api_requests_total{route,method,status}` | counter | how many requests the API served, by route template, method, and status |
| display-api | `display_api_request_seconds{route}` | histogram | how long the API took to send the response headers |
| display-api | `display_api_streams_active{aspect}` | gauge | how many capture responses the API is streaming now |
| display-api | `display_api_certificate_expiry_seconds` | gauge | when the serving certificate expires, in seconds since the epoch |

## The card observation

`display_observation_valid{source="card"}` is 1 when the last read of
the graphics card succeeded. The operator reads the card only while it
is connected to a compositor, so while the compositor is down, the
gauge keeps the value of the last read, and the age that you compute
from `display_observation_last_success_timestamp_seconds{source="card"}`
keeps growing.

## Compositor restart reasons

`display_compositor_restarts_total` counts each time the operator ends
the compositor so that it restarts, with the cause in the `reason`
label:

| `reason` | Why the operator restarts the compositor |
| --- | --- |
| `mode` | A claim's `mode` parameter or a `Display`'s `spec.mode` changes the mode that a screen runs. Weston reads its configuration only at startup. |
| `heal` | The compositor created an output again with another monitor or another mode, so the canvases on the other screens have the wrong size. |
| `hung` | The compositor accepted connections on its socket and did not answer for 10 seconds. The operator sends `SIGKILL`. |
| `masterless` | A read of the card showed that the compositor has no DRM master, so it cannot show a frame. The operator restarts each compositor process for this reason at most once. |

After each of these restarts, the compositor's container starts
weston again itself, so the kubelet's restart count does not include
them. A compositor that exits on its own takes its container with it,
and `display_compositor_container_restarts_total` counts that exit
from the kubelet's restart count.
