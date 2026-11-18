---
title: Metrics
weight: 30
toc: true
---

# Metrics

The operator's `Deployment` serves Prometheus metrics on port 9260, the
port named `metrics`. The pods of the CEC `DaemonSet` serve no metrics.
The base works without Prometheus. If you run the prometheus-operator,
add the `deploy/monitoring` component, whose `PodMonitor` scrapes the
operator.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| equipment-operator | `equipment_receiver_connected{receiver}` | gauge | one while the operator's last exchange with the receiver succeeded, zero otherwise |
| equipment-operator | `equipment_observation_valid{source}`, `equipment_observation_last_success_timestamp_seconds{source}` | gauge | whether the connection to the receiver is up, and when it last was |
| equipment-operator | `equipment_receiver_power{receiver}`, `equipment_receiver_volume{receiver}` | gauge | the receiver's power and volume |
| equipment-operator | `equipment_receiver_input_info{receiver, input}` | gauge, info | which input is selected |
| equipment-operator | `equipment_commands_total{status}` | counter | commands sent, by outcome: `ok`, `failed`, or `timeout` |
| equipment-operator | `equipment_wiim_temperature_celsius{receiver, sensor}` | gauge | the temperatures of a WiiM amp, where `sensor` is `cpu` or `board` |
| equipment-operator | `equipment_wiim_wifi_signal_dbm{receiver}`, `equipment_wiim_wifi_noise_dbm{receiver}`, `equipment_wiim_wifi_snr_db{receiver}`, `equipment_wiim_wifi_bitrate_bits_per_second{receiver}` | gauge | the Wi-Fi link of a WiiM amp that is on Wi-Fi |
| equipment-operator | `equipment_wiim_remote_battery_ratio{receiver}`, `equipment_wiim_remote_signal_dbm{receiver}` | gauge | the charge, from 0 to 1, and the signal strength of the remote that is connected to a WiiM amp |
| equipment-operator | `equipment_reconcile_duration_seconds{kind}` | histogram | how long one reconcile pass takes, by resource kind |
| equipment-operator | `equipment_reconcile_errors_total{kind}` | counter | reconcile passes that failed, by resource kind |
| equipment-operator | `equipment_watch_restarts_total{kind}` | counter | watches that closed and were opened again, by resource kind |

`equipment_receiver_connected` covers every receiver the operator
controls, whatever its protocol. The `source` label of the
`equipment_observation_*` gauges is the name of the `Receiver`.
`equipment_observation_valid` is one while the connection works and
zero when it drops, and
`equipment_observation_last_success_timestamp_seconds` is the time of
the last read made while the connection worked.

The outcomes of `equipment_commands_total` mean the following:

* `ok`: the operator wrote the command to the receiver's socket. The
  Denon protocol does not acknowledge a command, so `ok` does not mean
  that the receiver acted on it.
* `failed`: the operator dropped the command before it sent it, or the
  write returned an error.
* `timeout`: a session stopped waiting for the power-on command that it
  sends before it selects the input.

The `equipment_wiim_*` gauges hold values from the amp's `getStatusEx`
that change on their own between reads. They are not in the
`Receiver`'s `status.wiim`, because then the operator would write the
`Receiver` on every read. The operator sets each gauge when it reads
the amp, every 10 seconds, and deletes a series when the amp stops
reporting it: the Wi-Fi series when the amp uses its wired port, and
the remote series when no remote is connected. While the amp is
unreachable, it has no `equipment_wiim_*` series.

The amp reports its link rate in Mbit/s and the remote's charge as a
percentage. The operator converts them to bits per second and to a
ratio. No WiiM document states these units, but the values that amps
report fit them.

```yaml
resources:
  - https://github.com/liken-sh/liken//equipment-operator/deploy?ref=<version>
components:
  - https://github.com/liken-sh/liken//equipment-operator/deploy/monitoring?ref=<version>
```
