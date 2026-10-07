---
title: Metrics
weight: 40
toc: true
---

# Metrics

Three processes serve Prometheus metrics at `/metrics`, under `liken`'s [shared
contract](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md).
The `operator` container serves plain HTTP on port 9200, one per
node. The `capture` container serves them on port 9201, the same TLS
port as the taps, and the route needs no token. The `audio-api`
`Deployment` serves plain HTTP on port 9200, once for the cluster.
The base applies with no Prometheus in the cluster. An owner who runs
the prometheus-operator adds the `deploy/monitoring` component beside
the base, which holds a `PodMonitor` for each port.

Each process also sets `liken_build_info{component, version}` to 1.
The `component` is `audio-operator`, `audio-capture`, or `audio-api`.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| audio-operator | `audio_endpoint_connected{endpoint}` | gauge | physical link |
| audio-operator | `audio_endpoint_ready{endpoint}` | gauge | PipeWire node present |
| audio-operator | `audio_endpoint_claimed{endpoint}` | gauge | claimed but unusable is the alert |
| audio-operator | `audio_observation_valid{source}`, `audio_observation_last_success_timestamp_seconds{source}` | gauge | PipeWire or BlueZ stopped answering |
| audio-operator | `audio_control_failures_total{operation}` | counter | volume writes that fail |
| audio-operator | `audio_reconcile_duration_seconds{kind}` | histogram | how long one reconcile pass of one `Sink` or `Source` took |
| audio-operator | `audio_reconcile_errors_total{kind}` | counter | reconcile passes that ended in an error |
| audio-operator | `audio_watch_restarts_total{kind}` | counter | watches the API server closed that the operator opened again |
| audio-capture | `audio_capture_ready` | gauge | the container holds a server certificate and can answer a tap |
| audio-capture | `audio_captures_active{aspect}` | gauge | taps running on the node now |
| audio-capture | `audio_capture_bytes_total{aspect,format}`, `audio_capture_seconds_total{aspect,format}` | counter | how much sound the taps sent, and how long they held streams open |
| audio-capture | `audio_capture_failures_total{reason}` | counter | taps that failed |
| audio-api | `audio_api_requests_total{route,method,status}` | counter | requests answered |
| audio-api | `audio_api_request_seconds{route}` | histogram | time to the response headers |
| audio-api | `audio_api_streams_active{aspect}` | gauge | capture streams the API relays now |
| audio-api | `audio_api_certificate_expiry_seconds` | gauge | when the first certificate of the API runs out, in seconds since the epoch |

```yaml
resources:
  - https://github.com/liken-sh/liken//audio-operator/deploy?ref=<version>
components:
  - https://github.com/liken-sh/liken//audio-operator/deploy/monitoring?ref=<version>
```
