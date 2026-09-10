# Prometheus metrics

Plan 08. Built and drilled on liken-1 on 2026-09-10.

The contract for every metric here, the three layers, the names, the
port table, and the monitoring component, is [liken milestone
65](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md).
This plan states only what this operator adds.

## The problem

An operator pod can be healthy while a claimed endpoint cannot produce
sound. A speaker can lose its link, or PipeWire can lose the node for a
connected output. Pod health describes neither.

## The design

Layers 1 and 2 under the `audio_` prefix, on port 9200. The hardware
triple, with `ready` added beside `connected` because this operator has
two levels of presence.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| audio-operator | `audio_endpoint_connected{endpoint}` | gauge | physical link |
| audio-operator | `audio_endpoint_ready{endpoint}` | gauge | PipeWire node present |
| audio-operator | `audio_endpoint_claimed{endpoint}` | gauge | claimed but unusable is the alert |
| audio-operator | `audio_observation_valid{source}`, `audio_observation_last_success_timestamp_seconds{source}` | gauge | PipeWire or BlueZ stopped answering |
| audio-operator | `audio_control_failures_total{operation}` | counter | volume writes that fail |

The `endpoint` label is the Sink or Source name. The bluetooth-operator
owns battery and radio link; this operator never exports a second copy.

The monitoring component at `deploy/monitoring/` holds one PodMonitor.

## Proof

Failing tests first: connected without a node, disconnected, unclaimed,
unknown, recovered. On liken-1, claim a speaker and power it off, and
read connected fall while claimed holds.