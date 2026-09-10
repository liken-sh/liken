---
title: Metrics
weight: 40
toc: true
---

# Metrics

The operator serves Prometheus metrics at `/metrics` on port 9200, one
per node, under liken's [shared
contract](https://github.com/liken-sh/liken/blob/main/plans/65-prometheus-metrics.md).
The base applies with no Prometheus in the cluster. An owner who runs
the prometheus-operator adds the `deploy/monitoring` component beside
the base, which holds a `PodMonitor` for the port.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| display-operator | `display_output_connected{output}` | gauge | a monitor that went away |
| display-operator | `display_output_claimed{output}` | gauge | the hardware triple |
| display-operator | `display_observation_valid{source}`, `display_observation_last_success_timestamp_seconds{source}` | gauge | the card or the compositor socket stopped answering |
| display-operator | `display_output_mode_info{output, mode, refresh}` | gauge, info | what is actuated on each panel |
| display-operator | `display_compositor_restarts_total{reason}` | counter | mode prepare vs crash; the unbounded-restart problem as a rate |
| display-operator | `display_surfaces{output}` | gauge | what the compositor holds; a stuck surface |
| display-operator | `display_panel_power{output}`, `display_panel_brightness{output}` | gauge | the DDC state the idle screen drives |

```yaml
resources:
  - https://github.com/liken-sh/display-operator//deploy?ref=<version>
components:
  - https://github.com/liken-sh/display-operator//deploy/monitoring?ref=<version>
```
