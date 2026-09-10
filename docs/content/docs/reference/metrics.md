---
title: Metrics
weight: 28
toc: true
---

# Metrics

The machine operator and the cluster operator each serve Prometheus
metrics on port 9200. Both paths are `/metrics`.
The base needs no Prometheus: the operators serve these ports whether
or not anything reads them, and a cluster with no monitoring stack is
complete. An owner who runs the prometheus-operator adds the
`monitoring` component, which carries a `PodMonitor` for each
operator and two Grafana dashboards.

## Ports

Every process serves `/metrics` on port 9200. A pod on the cluster
network has a port space of its own, so one number serves every
process. A pod on the host network shares the node's port space, so it
takes a port nobody else on the host holds: the machine operator holds
9200 there, and the bluetooth operator 9250.

## Metrics

Every liken operator publishes the runtime layer that the Prometheus
client library supplies, `go_*` and `process_*`, and these:

| Component | Metric | Type | What it says |
| --- | --- | --- | --- |
| both | `liken_build_info{component, version}` | gauge, info | the release each component runs |
| both | `liken_reconcile_duration_seconds{kind}` | histogram | how long one reconcile pass takes |
| both | `liken_reconcile_errors_total{kind}` | counter | passes that failed |
| both | `liken_watch_restarts_total{kind}` | counter | watches that closed and opened again |

The machine operator and the cluster operator each publish the
metrics of their own domain:

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| machine-operator | `liken_release_info{version, slot}` | gauge, info | release per node; fleet skew |
| machine-operator | `liken_machine_boot_timestamp_seconds` | gauge | uptime; reboot count over time |
| machine-operator | `liken_machine_change_pending{tier}` | gauge | `restart`, `reboot`; waiting on approval |
| machine-operator | `liken_machine_converged` | gauge | spec matches what runs |
| machine-operator | `liken_release_download_bytes_total` | counter | staging progress |
| machine-operator | `liken_release_download_failures_total` | counter | a download that keeps failing |
| machine-operator | `liken_devices{class}` | gauge | a missing GPU or adapter shows as a drop |
| machine-operator | `liken_last_crash_timestamp_seconds` | gauge | pstore capture; a crash loop is a line |
| cluster-operator | `liken_machines{phase}` | gauge | fleet by phase |
| cluster-operator | `liken_disruption_approvals_pending` | gauge | approvals outstanding |
| cluster-operator | `liken_machines_behind_target` | gauge | nodes not yet on the target release |

## The monitoring component

Add this line to the fleet repository's kustomization, beside the
resources it already applies:

```yaml
components:
  - https://github.com/liken-sh/liken//monitoring?ref=2026.09.10-001
```
