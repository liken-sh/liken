---
title: Metrics
weight: 60
toc: true
---

# Metrics

The operator and the media browser each serve Prometheus metrics on
port 9200, and the catalog pod's Corrosion agent serves its own on its
own telemetry port, under [liken milestone
65](https://github.com/liken-sh/liken/blob/main/plans/completed/65-prometheus-metrics.md)'s
contract. The base applies with no Prometheus in the cluster. An owner
who runs the prometheus-operator adds the `deploy/monitoring` component
beside the base, which holds a `PodMonitor` for each of the three.

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| library-operator | `library_run_duration_seconds{library, worker}` | histogram | a worker whose run got slow |
| library-operator | `library_run_last_success_timestamp_seconds{library, worker}` | gauge | a worker that stopped finishing |
| library-operator | `library_items{library, kind}` | gauge | catalog size over time |
| library-operator | `library_fact_gap{library, fact}` | gauge | a fact whose gap never closes |
| library-operator | `library_attempts_total{library, fact, result}` | counter | a fact that stopped finding anything |
| library-operator | `library_provider_requests_total{library, provider, status}` | counter | a provider that started refusing |
| library-operator | `library_provider_request_seconds_total{library, provider}` | counter | the time one provider costs a run |
| library-operator | `library_trailer_fetches_total{library, site, result}` | counter | a site whose downloads started failing |
| library-operator | `library_trailer_fetch_bytes_total{library, site}` | counter | what a library's trailers cost the volume |
| media-browser | `library_browser_frame_seconds` | histogram | the covered-spin problem as a graph |
| media-browser | `library_browser_art_cache_bytes` | gauge | the RSS problem as a graph |
| catalog | upstream `corro_*` | upstream | sync and change health |

```yaml
resources:
  - https://github.com/liken-sh/library-operator//deploy?ref=<ref>
components:
  - https://github.com/liken-sh/library-operator//deploy/monitoring?ref=<ref>
```
