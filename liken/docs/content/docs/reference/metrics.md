---
title: Metrics
weight: 28
toc: true
---

# Metrics

The machine operator and the cluster operator each serve Prometheus
metrics at `/metrics` on port 9200. A cluster does not need Prometheus
to run. If you run the prometheus-operator, add
[the `monitoring` component](#the-monitoring-component), which has a
`PodMonitor` for each operator and Grafana dashboards. On a cluster
with more than one leader, each leader also serves
[the metrics of its etcd member](#etcd).

## Ports

Every `liken` operator serves `/metrics` on port 9200 by default. A
pod on the cluster network has its own port space, so every such pod
can use the same port. A pod on the host network shares the node's
port space, so it needs a port that nothing else on the host uses. The
machine operator runs on the host network with 9200, and the bluetooth
operator uses 9250.

The machine operator also serves `/healthz` on its port. It returns
`500` when the reconcile loop has been busy for more than 60 seconds.
At the same limit the operator stops renewing its heartbeat and exits,
and the kubelet starts it again. `/healthz` is for a person or a
monitor to read. The pod has no probe that uses it.

## Metrics

Every `liken` operator serves the `go_*` and `process_*` metrics that
the Prometheus client library provides, and these:

| Component | Metric | Type | Meaning |
| --- | --- | --- | --- |
| both | `liken_build_info{component, version}` | gauge, info | the version that the component runs |
| both | `liken_reconcile_duration_seconds{kind}` | histogram | how long one reconcile pass takes, in seconds |
| both | `liken_reconcile_errors_total{kind}` | counter | reconcile passes that failed |
| both | `liken_watch_restarts_total{kind}` | counter | times a watch closed and was opened again |

The `kind` label of `liken_watch_restarts_total` is the kind of object
that the watch reads. The machine operator watches `Machine`, `Node`,
`Cluster`, `Secret`, `Pod`, `ResourceSlice`, and `Disruption`. The
cluster operator watches `Machine`, `Cluster`, `Lease`, `DaemonSet`,
`Pod`, `Disruption`, and `MachineDisruptionBudget`. Each kind reports
0 until its first restart.

Each operator also serves metrics of its own:

| Component | Metric | Type | Meaning |
| --- | --- | --- | --- |
| machine-operator | `liken_release_info{version, slot}` | gauge, info | the release and boot slot that each machine runs, so you can see when machines run different releases |
| machine-operator | `liken_machine_boot_timestamp_seconds` | gauge | when the machine booted, as a Unix timestamp, so you can graph uptime and reboots |
| machine-operator | `liken_machine_change_pending{tier}` | gauge | staged changes that wait for a disruption, by the `tier` that applies them (`restart` or `reboot`) |
| machine-operator | `liken_machine_converged` | gauge | 1 when what the machine runs matches its spec |
| machine-operator | `liken_release_download_bytes_total` | counter | release artifact bytes that this machine downloaded and verified |
| machine-operator | `liken_release_download_failures_total` | counter | release downloads that failed |
| machine-operator | `liken_devices{class}` | gauge | the number of devices of each class, so a missing GPU or adapter shows as a drop |
| machine-operator | `liken_last_crash_timestamp_seconds` | gauge | when the newest kernel crash that pstore still has a record of happened, as a Unix timestamp |
| machine-operator | `liken_machine_longest_pass_seconds` | gauge | the longest reconcile pass since the operator started, in seconds. The operator stops its heartbeat at 60 |
| machine-operator | `liken_machine_backstop_repairs_total{step}` | counter | writes made by the backstop pass that runs every five minutes, by `step`. Each write means that the state changed and no event started a pass for it |
| machine-operator | `liken_machine_passes_total{cause}` | counter | reconcile passes, by the `cause` that started each one. A machine that has converged runs few |
| machine-operator | `liken_time_offset_seconds` | gauge | the clock's error in seconds. See [the clock](#the-clock) |
| machine-operator | `liken_time_last_sync_timestamp_seconds` | gauge | when a time source last responded, as a Unix timestamp |
| machine-operator | `liken_wireless_signal_dbm{interface,bssid}` | gauge | the signal level of each radio's access point. See [the radios](#the-radios) |
| machine-operator | `liken_wireless_tx_bitrate_bits_per_second{interface,bssid}` | gauge | the bitrate that each radio last sent at |
| cluster-operator | `liken_machines{phase}` | gauge | the number of machines in each phase |
| cluster-operator | `liken_disruption_approvals_pending` | gauge | machines that have a staged change and wait for a person to approve it |
| cluster-operator | `liken_machines_behind_target` | gauge | machines that do not run the target release yet |

## The clock

`kubectl get machine` shows whether the clock is synchronized now,
and a limit on its error:

```
NAME      ROLE       STATUS   ...   TIME           OFFSET
liken-1   leader     Ready    ...   Synchronized   ±5ms
```

The limit is one of `±1ms`, `±5ms`, `±25ms`, `±128ms`, `±1s`, or
`>1s`, and it is
[`status.time.offset`](/docs/reference/machine/#statustime). The
condition `ClockSynchronized` is `False` when no time source responded
in the last three measurements, or when the limit is `±1s` or worse,
which is more error than a slew can correct. Each change of the condition posts an `Event`,
and `init` prints a console line each time the limit changes. The
condition does not count toward `Ready`, because an outage of the
upstream time source affects every leader at once.

To see how the clock moved over time, graph
`liken_time_offset_seconds`, the exact error, which `init` measures
every 64 seconds. Status has only the limit, because the exact error
changes at every measurement and a status write on every measurement
would load the API server and the datastore. The machine operator reads
the measurement only when Prometheus scrapes. The series is absent when
the last response is more than three measurements old, and
`liken_time_last_sync_timestamp_seconds` then shows when the source
last responded. The fleet dashboard graphs the offset of every machine.

To read the exact error now without Prometheus, read the machine
operator's endpoint through the API server's proxy:

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
uses the one with the smallest error limit. The limit of a measurement
is half its round trip time, which covers a path that is slower in one
direction, plus 100 ppm of its age, which covers the drift of the
clock since. On wifi, single exchanges can vary by tens of
milliseconds, and this filter keeps that noise out of the clock, the
limit, and the metric.

## The radios

`status.network.interfaces[].wireless` shows the access point that
each radio is associated with, and it changes only when the radio moves
to another access point. The signal changes all the time, so it is a
metric and not a status field. The machine operator reads the signal
from each radio's `wpa_supplicant` only when Prometheus scrapes. The
driver measures the signal on frames that the radio already receives,
so a scrape sends nothing over the air.

`liken_wireless_signal_dbm` is the signal level of the access point,
and `liken_wireless_tx_bitrate_bits_per_second` is the bitrate that the
radio last sent at. The `bssid` label is the access point's address, so
a move to another access point shows in a graph as one series that ends
and another that starts. Both series are absent while the radio is not
associated. While the signal is below -70 dBm, the machine scans for
a stronger access point every 30 seconds.

Without Prometheus, read the same endpoint through the API server, as
for the clock, and filter for `^liken_wireless_`.

## etcd

A cluster with more than one leader keeps its datastore in etcd, and
each leader runs one etcd member inside k3s. Each member serves etcd's
own Prometheus metrics at `http://<node IP>:2381/metrics`. Scrape them
to see whether a slow control plane has a slow disk or a slow network
under etcd. For example, `etcd_disk_wal_fsync_duration_seconds` is how
long each write to etcd's log waits for the disk, and
`etcd_network_peer_round_trip_time_seconds` is the delay between two
members. etcd names and describes these series in
[its own documentation](https://etcd.io/docs/latest/metrics/).

The port is plain HTTP with no authentication, as the operators' ports
are. etcd listens on the leader's node IP and on the loopback address,
and on no other address of the machine. The port serves `/metrics` and
etcd's health paths, `/health`, `/livez`, and `/readyz`. The metrics
show the member IDs, the size of the datastore, and the counts and
durations of requests. They show no key and no value of the datastore.
A pod on the host network of a leader cannot listen on 2381.

A cluster with one leader keeps its datastore in sqlite and runs no
etcd, so no machine listens on the port there. A follower never
listens on it.

k3s sets the label `node-role.kubernetes.io/etcd=true` on the `Node`
of each etcd member. This Prometheus scrape job finds every `Node`,
keeps the ones with that label, and scrapes port 2381 at the
`InternalIP` of each. It gives each series a `node` label, as the
`PodMonitor`s of the monitoring component do:

```yaml
scrape_configs:
  - job_name: etcd
    kubernetes_sd_configs:
      - role: node
    relabel_configs:
      - source_labels: [__meta_kubernetes_node_label_node_role_kubernetes_io_etcd]
        regex: "true"
        action: keep
      - source_labels: [__meta_kubernetes_node_address_InternalIP]
        target_label: __address__
        replacement: $1:2381
      - source_labels: [__meta_kubernetes_node_name]
        target_label: node
```

With the prometheus-operator, apply the same job as a `ScrapeConfig`.
The monitoring component does not include it, because `ScrapeConfig`
is a `v1alpha1` kind that an older prometheus-operator does not serve.
Your `Prometheus` reads the object only when its
`spec.scrapeConfigSelector` selects it, so add the labels that your
selector needs:

```yaml
apiVersion: monitoring.coreos.com/v1alpha1
kind: ScrapeConfig
metadata:
  name: liken-etcd
  namespace: monitoring
spec:
  jobName: etcd
  kubernetesSDConfigs:
    - role: Node
  relabelings:
    - sourceLabels: [__meta_kubernetes_node_label_node_role_kubernetes_io_etcd]
      regex: "true"
      action: keep
    - sourceLabels: [__meta_kubernetes_node_address_InternalIP]
      targetLabel: __address__
      replacement: $1:2381
    - sourceLabels: [__meta_kubernetes_node_name]
      targetLabel: node
```

Both jobs need a Prometheus whose `ServiceAccount` can list and watch
`Node`s, which the kube-prometheus-stack grants.

To read the metrics of one member without Prometheus, run `curl` in a
pod:

```sh
kubectl run etcd-metrics --rm -i --restart=Never \
  --image=curlimages/curl --command -- \
  curl -s http://<node IP>:2381/metrics | grep '^etcd_disk_wal_fsync'
```

## The monitoring component

Add this to the `kustomization.yaml` of the fleet repository, beside
the resources that it already applies:

```yaml
components:
  - https://github.com/liken-sh/liken//liken/deploy/monitoring?ref=<version>
```

Set `<version>` to the release that the fleet runs.
