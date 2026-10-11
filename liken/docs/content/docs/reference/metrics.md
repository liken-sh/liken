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

The machine operator also serves `/healthz` on its port. It answers
`500` once the operator's reconcile loop has been busy for 60 seconds.
At the same limit the operator stops renewing its heartbeat and ends its
own process, so the kubelet starts it again. `/healthz` is for a person
or a monitor to read, and no probe depends on it.

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
| machine-operator | `liken_machine_longest_pass_seconds` | gauge | the longest pass since the operator started; the heartbeat stops at 60 |
| machine-operator | `liken_machine_backstop_repairs_total{step}` | counter | a write by the five-minute backstop pass, which names a missing wake |
| machine-operator | `liken_machine_passes_total{cause}` | counter | passes by what started each one; a settled machine runs few |
| machine-operator | `liken_time_offset_seconds` | gauge | the clock's exact error; see [the clock](#the-clock) |
| machine-operator | `liken_time_last_sync_timestamp_seconds` | gauge | when a time source last answered |
| cluster-operator | `liken_machines{phase}` | gauge | fleet by phase |
| cluster-operator | `liken_disruption_approvals_pending` | gauge | approvals outstanding |
| cluster-operator | `liken_machines_behind_target` | gauge | nodes not yet on the target release |

## The clock

You can see a machine's clock in three places, and each one answers a
different question.

**Is the clock fine now?** `kubectl get machine` shows the clock's
state and a bound on its error:

```
NAME      ROLE       STATUS   ...   TIME           OFFSET
liken-1   leader     Ready    ...   Synchronized   ±5ms
```

The bound is one of `±1ms`, `±5ms`, `±25ms`, `±128ms`, `±1s`, or
`>1s`, and it is
[`status.time.offset`](/docs/reference/machine/#statustime). The
condition `ClockSynchronized` is `False` when the time sources stop
answering, or when the bound is `±1s` or worse, which is more error
than a slew can correct. Each change of the condition posts an event,
and `init` prints a console line each time the bound changes. The
condition does not count toward `Ready`, because an upstream outage
reaches every leader at once.

**How has the clock moved?** `liken_time_offset_seconds` is the exact
error, measured every 64 seconds. Status holds only the bound because
the exact error changes at every measurement, and each status write
costs the API server and etcd. The machine operator reads the
measurement only when Prometheus scrapes, so the metric costs nothing
while nobody scrapes it. The series is absent when the last answer is
more than three measurements old, and
`liken_time_last_sync_timestamp_seconds` then shows when the source
went quiet. The fleet dashboard draws the offset of every machine.

**What is the exact error now?** Without Prometheus, read the same
endpoint through the API server:

```sh
pod=$(kubectl -n liken-system get pod -o name \
  -l app.kubernetes.io/name=liken-machine-operator \
  --field-selector spec.nodeName=<machine>)
kubectl get --raw \
  "/api/v1/namespaces/liken-system/pods/${pod#pod/}:9200/proxy/metrics" \
  | grep '^liken_time_'
```

Each measurement is one SNTP exchange. `init` keeps the last eight,
corrects each one for the slews applied since it was measured, and
uses the one with the smallest error bound: half its round trip, for
the asymmetry of the path, plus 100 ppm of its age, for the drift of
the clock since. On wifi, single exchanges vary by tens of
milliseconds, and this filter keeps that noise out of the clock, the
bound, and the metric.

## The monitoring component

Add this line to the fleet repository's kustomization, beside the
resources it already applies:

```yaml
components:
  - https://github.com/liken-sh/liken//liken/deploy/monitoring?ref=<version>
```

Set `<version>` to the release that the fleet runs.
