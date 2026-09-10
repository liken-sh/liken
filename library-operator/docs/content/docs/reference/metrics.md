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
| library-operator | `library_scan_duration_seconds{library}` | histogram | a scan that got slow |
| library-operator | `library_scan_last_success_timestamp_seconds{library}` | gauge | a scan that stopped finishing |
| library-operator | `library_items{library, kind}` | gauge | catalog size over time |
| media-browser | `library_browser_frame_seconds` | histogram | the covered-spin problem as a graph |
| media-browser | `library_browser_art_cache_bytes` | gauge | the RSS problem as a graph |
| catalog | upstream `corro_*` | upstream | sync and change health |

```yaml
resources:
  - https://github.com/liken-sh/library-operator//deploy?ref=<ref>
components:
  - https://github.com/liken-sh/library-operator//deploy/monitoring?ref=<ref>
```
