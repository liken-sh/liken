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
| audio-operator | `audio_endpoint_connected{endpoint}` | gauge | physical link |
| audio-operator | `audio_endpoint_ready{endpoint}` | gauge | PipeWire node present |
| audio-operator | `audio_endpoint_claimed{endpoint}` | gauge | claimed but unusable is the alert |
| audio-operator | `audio_observation_valid{source}`, `audio_observation_last_success_timestamp_seconds{source}` | gauge | PipeWire or BlueZ stopped answering |
| audio-operator | `audio_control_failures_total{operation}` | counter | volume writes that fail |

```yaml
resources:
  - https://github.com/liken-sh/audio-operator//deploy?ref=<version>
components:
  - https://github.com/liken-sh/audio-operator//deploy/monitoring?ref=<version>
```
