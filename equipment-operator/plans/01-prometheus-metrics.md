# Prometheus metrics

Plan 01. Proposed.

The contract for every metric here, the three layers, the names, the
port table, and the monitoring component, is [liken milestone
65](https://github.com/liken-sh/liken/blob/main/plans/65-prometheus-metrics.md).
This plan states only what this operator adds.

## The problem

The receiver is the one device in the room that liken talks to over the
network rather than a cable, and the one that tells us nothing when it
stops answering.

## The design

Layers 1 and 2 under the `equipment_` prefix, on port 9200.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| equipment-operator | `equipment_receiver_connected{receiver}` | gauge | the Denon answers |
| equipment-operator | `equipment_receiver_claimed{receiver}` | gauge | the hardware triple |
| equipment-operator | `equipment_observation_valid{source}`, `equipment_observation_last_success_timestamp_seconds{source}` | gauge | the session went stale |
| equipment-operator | `equipment_receiver_power{receiver}`, `equipment_receiver_volume{receiver}` | gauge | what the room is set to |
| equipment-operator | `equipment_receiver_input_info{receiver, input}` | gauge, info | which input is selected |
| equipment-operator | `equipment_commands_total{status}` | counter | commands that fail |

The monitoring component at `deploy/monitoring/` holds one PodMonitor.

## Proof

Failing tests first. On liken-1, power the receiver off at the wall and
read connected fall.