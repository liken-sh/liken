---
title: Metrics
weight: 30
toc: true
---

# Metrics

The operator serves Prometheus metrics on port 9200. The base needs no
Prometheus at all; an owner who runs the prometheus-operator adds the
`deploy/monitoring` component to scrape it.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| equipment-operator | `equipment_receiver_connected{receiver}` | gauge | one while the operator has an answered exchange with the receiver, zero otherwise |
| equipment-operator | `equipment_observation_valid{source}`, `equipment_observation_last_success_timestamp_seconds{source}` | gauge | whether the driver connection to the receiver is up, and when it was last up |
| equipment-operator | `equipment_receiver_power{receiver}`, `equipment_receiver_volume{receiver}` | gauge | what the room is set to |
| equipment-operator | `equipment_receiver_input_info{receiver, input}` | gauge, info | which input is selected |
| equipment-operator | `equipment_commands_total{status}` | counter | commands by outcome: `ok`, `failed`, or `timeout` |
| equipment-operator | `equipment_reconcile_duration_seconds{kind}` | histogram | how long one reconcile pass takes, by resource kind |
| equipment-operator | `equipment_reconcile_errors_total{kind}` | counter | reconcile passes that failed, by resource kind |
| equipment-operator | `equipment_watch_restarts_total{kind}` | counter | watches that closed and opened again, by resource kind |

The `receiver_connected` gauge covers every receiver the operator
drives, whatever its protocol. The `source` label of the two
`observation_*` gauges holds the name of the `Receiver`. Both gauges
follow the driver connection: `observation_valid` is one while the
connection answers and zero when it drops, and the timestamp gauge
holds the time of the last read while the connection was up. The
`ok` outcome means a command reached the receiver's socket, not that
the receiver acted on it. The `timeout` outcome counts the one wait
the operator gives up on: the power-on that a session sends before it
selects the input.

```yaml
resources:
  - https://github.com/liken-sh/liken//equipment-operator/deploy?ref=<version>
components:
  - https://github.com/liken-sh/liken//equipment-operator/deploy/monitoring?ref=<version>
```
