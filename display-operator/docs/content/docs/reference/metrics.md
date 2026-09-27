---
title: Metrics
weight: 40
toc: true
---

# Metrics

The operator serves Prometheus metrics at `/metrics` on port 9200, one
per node, under liken's [shared
contract](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md).
The base applies with no Prometheus in the cluster. An owner who runs
the prometheus-operator adds the `deploy/monitoring` component beside
the base, which holds a `PodMonitor` for the port.

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

```yaml
resources:
  - https://github.com/liken-sh/display-operator//deploy?ref=<version>
components:
  - https://github.com/liken-sh/display-operator//deploy/monitoring?ref=<version>
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
