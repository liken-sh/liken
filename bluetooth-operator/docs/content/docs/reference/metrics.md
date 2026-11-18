---
title: Metrics
weight: 40
toc: true
---

# Metrics

The operator serves Prometheus metrics at `/metrics` on port `9250`.
The manifests in `deploy` do not need Prometheus. If you run the
prometheus-operator, add the `deploy/monitoring` component next to
`deploy`, as shown below, so that Prometheus scrapes the operator.

| Metric | Type | What it reports |
| --- | --- | --- |
| `bluetooth_peripheral_connected{peripheral}` | gauge | 1 while the `Peripheral` has a link to its adapter, 0 while it does not. |
| `bluetooth_peripheral_claimed{peripheral}` | gauge | 1 while a prepared claim holds the controller, 0 while none does. |
| `bluetooth_observation_valid{source}` | gauge | 1 while the last read of the source succeeded, 0 while it failed. The sources are BlueZ and the kernel's battery interface. |
| `bluetooth_observation_last_success_timestamp_seconds{source}` | gauge | When a read of the source last succeeded, in seconds since the epoch. Compare it with the current time to alert on a source that stopped updating. |
| `bluetooth_peripheral_battery_percent{peripheral}` | gauge | The charge the device last reported. The series is absent when no source reports a level. |
| `bluetooth_peripheral_bonded{peripheral}` | gauge | 1 while `bluetoothd` has a stored link key for the device, 0 while it has none. |
| `bluetooth_disconnects_total{peripheral, reason}` | counter | Links that went from connected to disconnected. `reason` is the reason of the `Connected` condition after the link ended, such as `Asleep` or `ClosedByRadio`. A rising count means the link keeps dropping. |
| `bluetooth_wake_presses_total{peripheral}` | counter | `KEY_UNKNOWN` presses that the operator wrote into the device's virtual input node when a remote connected after a sleep. |
| `bluetooth_pair_attempts_total{result}` | counter | Pairing calls to `bluetoothd`, by `result`: `paired` when it made the bond and `refused` when it did not. |
| `bluetooth_adapter_present` | gauge | 1 while `bluetoothd` reports an adapter on this node, 0 while it reports none. |
| `bluetooth_input_events_total{peripheral}` | counter | Input events that the operator forwarded from the device's real input node to its virtual one. A count that stays flat while someone presses buttons means presses do not reach the pod. |
| `bluetooth_reconcile_duration_seconds{kind}` | histogram | How long one reconcile pass over one resource kind took. |
| `bluetooth_reconcile_errors_total{kind}` | counter | Reconcile passes over one resource kind that ended in an error. |
| `liken_build_info{component, version}` | gauge | The release that the process runs. The value is always 1. |

```yaml
resources:
  - https://github.com/liken-sh/liken//bluetooth-operator/deploy?ref=<ref>
components:
  - https://github.com/liken-sh/liken//bluetooth-operator/deploy/monitoring?ref=<ref>
```
