---
title: Metrics
weight: 40
toc: true
---

# Metrics

The operator serves Prometheus metrics at `/metrics` from three
containers, following `liken`'s
[shared metrics contract](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md).

* The `operator` container runs on every node and serves plain HTTP on
  port 9200.
* The `capture` container runs on every node and serves HTTPS on port
  9201, the same port as the taps. The `/metrics` route needs no
  token.
* The `audio-api` `Deployment` runs once for the cluster and serves
  plain HTTP on port 9200.

The base installs without Prometheus. If your cluster runs the
prometheus-operator, add the `deploy/monitoring` component beside the
base. It has a `PodMonitor` for each port.

Each container also sets `liken_build_info{component, version}` to 1.
The `component` label is `audio-operator`, `audio-capture`, or
`audio-api`.

| Component | Metric | Type | What it shows |
| --- | --- | --- | --- |
| audio-operator | `audio_endpoint_connected{endpoint}` | gauge | 1 while the endpoint's physical link is present |
| audio-operator | `audio_endpoint_ready{endpoint}` | gauge | 1 while PipeWire has a node for the endpoint |
| audio-operator | `audio_endpoint_claimed{endpoint}` | gauge | 1 while a claim has the endpoint. Alert on an endpoint that is claimed and not ready |
| audio-operator | `audio_observation_valid{source}`, `audio_observation_last_success_timestamp_seconds{source}` | gauge | whether the last read of PipeWire or BlueZ succeeded, and when a read last succeeded. The `source` label is `pipewire` or `bluez` |
| audio-operator | `audio_control_failures_total{operation}` | counter | writes to hardware that failed. The `operation` label is `volume`, `control`, or `codec` |
| audio-operator | `audio_reconcile_duration_seconds{kind}` | histogram | how long one reconcile pass of one `Sink` or `Source` took. The `kind` label is `Sink` or `Source` |
| audio-operator | `audio_reconcile_errors_total{kind}` | counter | reconcile passes that ended in an error |
| audio-operator | `audio_watch_restarts_total{kind}` | counter | watches of `Sink`s or `Source`s that the API server closed and the operator opened again |
| audio-capture | `audio_capture_ready` | gauge | 1 while the container has a server certificate and can serve a tap |
| audio-capture | `audio_captures_active{aspect}` | gauge | taps running on the node now. The `aspect` label is `audio` |
| audio-capture | `audio_capture_bytes_total{aspect,format}`, `audio_capture_seconds_total{aspect,format}` | counter | the bytes of sound that the taps sent, and the seconds that they kept streams open. The `format` label is `wav`, `flac`, or `opus` |
| audio-capture | `audio_capture_failures_total{reason}` | counter | taps that failed. The `reason` label is `connect`, `target`, `wrong-target`, `encoder`, `limit`, or `certificate` |
| audio-api | `audio_api_requests_total{route,method,status}` | counter | requests that the API served |
| audio-api | `audio_api_request_seconds{route}` | histogram | time from the request to the response headers |
| audio-api | `audio_api_streams_active{aspect}` | gauge | capture streams that the API is relaying now |
| audio-api | `audio_api_certificate_expiry_seconds` | gauge | the time when the first certificate of the API expires, in seconds since the epoch |

To add the monitoring component, reference it from your `kustomize`
file:

```yaml
resources:
  - https://github.com/liken-sh/liken//audio-operator/deploy?ref=<version>
components:
  - https://github.com/liken-sh/liken//audio-operator/deploy/monitoring?ref=<version>
```
