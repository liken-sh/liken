---
title: Metrics
weight: 30
toc: true
---

The operator serves Prometheus metrics on port 9260. The base needs no
Prometheus at all; an owner who runs the prometheus-operator adds the
`deploy/monitoring` component to scrape it.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| equipment-operator | `equipment_receiver_connected{receiver}` | gauge | the Denon answers |
| equipment-operator | `equipment_receiver_claimed{receiver}` | gauge | the hardware triple |
| equipment-operator | `equipment_observation_valid{source}`, `equipment_observation_last_success_timestamp_seconds{source}` | gauge | the session went stale |
| equipment-operator | `equipment_receiver_power{receiver}`, `equipment_receiver_volume{receiver}` | gauge | what the room is set to |
| equipment-operator | `equipment_receiver_input_info{receiver, input}` | gauge, info | which input is selected |
| equipment-operator | `equipment_commands_total{status}` | counter | commands that fail |

```yaml
resources:
  - https://github.com/liken-sh/equipment-operator//deploy?ref=<version>
components:
  - https://github.com/liken-sh/equipment-operator//deploy/monitoring?ref=<version>
```
