---
title: Metrics
weight: 40
toc: true
---

The operator serves Prometheus metrics at `/metrics` on port `9250`.
The base applies with no Prometheus in the cluster at all. An owner
who runs the prometheus-operator adds the `deploy/monitoring`
component beside the base to scrape it.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| bluetooth-operator | `bluetooth_peripheral_connected{peripheral}` | gauge | the link |
| bluetooth-operator | `bluetooth_peripheral_claimed{peripheral}` | gauge | the hardware triple |
| bluetooth-operator | `bluetooth_observation_valid{source}`, `bluetooth_observation_last_success_timestamp_seconds{source}` | gauge | BlueZ or the kernel battery source went stale |
| bluetooth-operator | `bluetooth_peripheral_battery_percent{peripheral}` | gauge | the remote needs charging |
| bluetooth-operator | `bluetooth_peripheral_bonded{peripheral}` | gauge | the link key is gone from bluetoothd |
| bluetooth-operator | `bluetooth_disconnects_total{peripheral}` | counter | link flap |
| bluetooth-operator | `bluetooth_pair_attempts_total{result}` | counter | bluetoothd refuses the pairings a person opens |
| bluetooth-operator | `bluetooth_adapter_present` | gauge | the rtw88 wedge |
| bluetooth-operator | `bluetooth_input_events_total{peripheral}` | counter | the relay is passing presses |

```yaml
resources:
  - https://github.com/liken-sh/bluetooth-operator//deploy?ref=<ref>
components:
  - https://github.com/liken-sh/bluetooth-operator//deploy/monitoring?ref=<ref>
```
