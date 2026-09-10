# Prometheus metrics

Plan 07. Proposed.

The contract for every metric here, the three layers, the names, the
port table, and the monitoring component, is [liken milestone
65](https://github.com/liken-sh/liken/blob/main/plans/65-prometheus-metrics.md).
This plan states only what this operator adds.

## The problem

A remote can need charging while every pod is healthy, a speaker can
reconnect ten times an hour, and the adapter can wedge. Sleeping
controllers also disconnect as part of normal operation, so a disconnect
count needs a graph, not an alert.

## The design

Layers 1 and 2 under the `bluetooth_` prefix, on port 9250. This pod
runs on the host network beside the machine operator, which holds
9200 there, so this one takes a port of its own.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| bluetooth-operator | `bluetooth_peripheral_connected{peripheral}` | gauge | the link |
| bluetooth-operator | `bluetooth_peripheral_claimed{peripheral}` | gauge | the hardware triple |
| bluetooth-operator | `bluetooth_observation_valid{source}`, `bluetooth_observation_last_success_timestamp_seconds{source}` | gauge | BlueZ or the kernel battery source went stale |
| bluetooth-operator | `bluetooth_peripheral_battery_percent{peripheral}` | gauge | the remote needs charging |
| bluetooth-operator | `bluetooth_disconnects_total{peripheral}` | counter | link flap |
| bluetooth-operator | `bluetooth_adapter_present` | gauge | the rtw88 wedge |
| bluetooth-operator | `bluetooth_input_events_total{peripheral}` | counter | the relay is passing presses |

Battery is absent when no source reports a level; zero is an observed
empty battery. The `peripheral` label is the Peripheral resource's name.

The monitoring component at `deploy/monitoring/` holds one PodMonitor.

## Proof

Failing tests first. On liken-1, sleep and wake the remote and read one
disconnect; let it sit and read the battery line.