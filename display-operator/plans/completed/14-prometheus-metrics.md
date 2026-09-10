# Prometheus metrics

Plan 14. Built and drilled on liken-1 on 2026-09-10.

The contract for every metric here, the three layers, the names, the
port table, and the monitoring component, is [liken milestone
65](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md).
This plan states only what this operator adds.

## The problem

A panel can lose its cable, the compositor can restart on a stuck mode
prepare without bound, and a surface can outlive the claim that
delivered it. Each of these is a status field today, and none of them is
a line on a graph.

## The design

Layers 1 and 2 as milestone 65 states them, under the `display_` prefix,
on port 9200. The compositor container serves no metrics of its own; the
operator reports what it observes of it.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| display-operator | `display_output_connected{output}` | gauge | a monitor that went away |
| display-operator | `display_output_claimed{output}` | gauge | the hardware triple |
| display-operator | `display_observation_valid{source}`, `display_observation_last_success_timestamp_seconds{source}` | gauge | the card or the compositor socket stopped answering |
| display-operator | `display_output_mode_info{output, mode, refresh}` | gauge, info | what is actuated on each panel |
| display-operator | `display_compositor_restarts_total{reason}` | counter | mode prepare vs crash; the unbounded-restart problem as a rate |
| display-operator | `display_surfaces{output}` | gauge | what the compositor holds; a stuck surface |
| display-operator | `display_panel_power{output}`, `display_panel_brightness{output}` | gauge | the DDC state the idle screen drives |

`display_compositor_restarts_total` carries the reasons `mode` and
`heal`, the two restarts this operator orders. A `crash` reason waits
on a fact: nothing today tells a compositor that exited on its own
from one this operator ended, without a new correlation against the
Wayland session counter.

The monitoring component at `deploy/monitoring/` holds one PodMonitor
for the operator pod.

## Proof

Failing tests first, against a real registry: a connected output, one
that left, a compositor restart of each reason, and a repeated scrape
that changes no counter and reads no card. On liken-1, unplug a panel
and restart the compositor, and read both on the graph.