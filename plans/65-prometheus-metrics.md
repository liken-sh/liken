# Prometheus metrics

Milestone 65. Every long-running process that liken or a liken operator
ships serves Prometheus metrics on a port of its own. This document is
the contract the whole organization follows: the layers every process
exposes, the names, the labels, the port, and how a cluster owner
connects a Prometheus to them. It then states liken's own metrics, the
ones about the machine and the fleet.

## The problem

liken and its operators expose their state as resource status. A
Machine says which release it runs, a Display says which mode a panel
holds, a Peripheral says whether a remote is connected. Status is the
present. Nothing keeps the past: how often the remote drops its link,
how long the last upgrade took, whether a scan is getting slower, or
what a room's CPU did when a film started. Kubernetes, node_exporter,
and SMART exporters cover the generic machine. Nothing covers what
makes these processes different.

Every program in the organization is a hand-written client-go loop, or
a Rust process, so nothing comes free from a framework. The five
metrics plans the operators already hold each picked their own signals
and their own words for the same facts.

## The design

### Three layers in every process

**Layer 1, the runtime.** The Prometheus client's defaults: `go_*` and
`process_*` in Go, and `process_*` plus the tokio runtime in Rust. One
gauge on top of them:

```
liken_build_info{component="display-operator", version="2026.09.10-001"} 1
```

Every process uses this one name, with `component` set to its own name,
so a single panel shows every release in the cluster.

**Layer 2, the reconcile loop.** Every operator exposes the same three,
under its own prefix, labeled by the resource kind the loop serves:

```
<prefix>_reconcile_duration_seconds{kind}   histogram
<prefix>_reconcile_errors_total{kind}       counter
<prefix>_watch_restarts_total{kind}         counter
```

A reconcile that changes nothing still counts as a run. A watch that
the API closes and the operator opens again is one restart. A process
that has no reconcile loop, such as a screen client, has no layer 2.

**Layer 3, the domain.** The metrics that make the process what it is.
Each repository's plan lists them in one table with the columns
component, metric, type, and why. The tables for the operators are in
their own repositories; liken's is below.

### The hardware triple

Every operator that owns hardware has the same three facts, and uses
the same words for them:

```
<prefix>_<thing>_connected{<id>}                       gauge, 1 or 0
<prefix>_<thing>_claimed{<id>}                          gauge, 1 or 0
<prefix>_observation_valid{source}                      gauge, 1 or 0
<prefix>_observation_last_success_timestamp_seconds{source}
```

Connected says the thing is physically there. Claimed says a workload
holds it. A thing that is claimed and not connected is the alert; a
thing that is unplugged and unclaimed is not. A missing observation is
unknown, not disconnected: while `observation_valid` is 0, the
connected gauge keeps its last value, and the timestamp says how old
that value is.

### Names and labels

Prefixes, one per repository. people-operator ships a CRD and no
program, so it has no prefix and no metrics.

| Repository | Prefix |
| --- | --- |
| liken | `liken_` |
| display-operator | `display_` |
| media-operator, its command sidecar, idle-screen | `media_` |
| library-operator | `library_`, and `library_browser_` for the media browser |
| audio-operator | `audio_` |
| bluetooth-operator | `bluetooth_` |
| equipment-operator | `equipment_` |
| git-csi-driver | `gitcsi_` |
| per-node-csi-driver | `pernodecsi_` |

Names follow the
[Prometheus naming rules](https://prometheus.io/docs/practices/naming/):
a unit suffix, `_total` on counters, `_info` on a gauge whose value is
always 1 and whose labels carry the fact. Labels are bounded by
inventory: a device, an output, a library, a zone, a provider, a fixed
reason category. Never a UID, a pod name, a media title, a path, a
URL, or an error message. Prometheus target labels already carry the
node and the pod.

A gauge that reports a timestamp reports the event's own time, not an
age that counts up between scrapes.

### The listener

Every process serves `/metrics` on port 9200. A pod on the cluster
network has a port space of its own, so two pods on one node both
listen on 9200 with no conflict, and one number is one less thing to
remember.

A pod on the host network shares the node's port space with every
other host-network pod and with the node's own exporters, so it takes
a port nobody else on the host holds. Two pods run there today:

| Host-network process | Port |
| --- | --- |
| liken machine-operator | 9200 |
| bluetooth-operator | 9250 |

The listener takes its address the same way the process takes its
other settings. An empty address turns it off. The container port is
named `metrics` in the pod template. A scrape reads an in-memory
registry and never touches hardware, D-Bus, or the API. A failure in
the listener never blocks the work the process exists for.

### The monitoring component

Each repository ships its metrics wiring as a kustomize Component next
to its base:

```
deploy/
  kustomization.yaml       the base, as today
deploy/monitoring/
  kustomization.yaml       kind: Component
  podmonitor.yaml          a PodMonitor per pod that serves metrics
```

Every PodMonitor relabels the pod's node name into the target label
`node`, because a scrape of a pod on the host network or in a
DaemonSet is about the machine, and the fleet dashboard selects by
that label:

```yaml
podMetricsEndpoints:
  - port: metrics
    relabelings:
      - sourceLabels: [__meta_kubernetes_pod_node_name]
        targetLabel: node
```

The base applies on a cluster with no monitoring at all. An owner who
runs the prometheus-operator adds the component to their fleet
repository beside the base:

```yaml
resources:
  - https://github.com/liken-sh/display-operator//deploy?ref=2026.09.10-001
components:
  - https://github.com/liken-sh/display-operator//deploy/monitoring?ref=2026.09.10-001
```

liken has no deploy base, because it applies its own system pods. Its
component is at `monitoring/` in the repository root. Its PodMonitors
and its dashboard ConfigMaps are in the `monitoring` namespace, where
an owner's stack already looks, and the PodMonitors reach across into
`liken-system` with a namespace selector to find the two operators.

An owner on a plain Prometheus, Grafana Alloy, or VictoriaMetrics
ignores the component and writes a scrape configuration against the
port. The port and the names are the contract; the component is one
convenience for the most common stack.

### Dashboards

liken ships two Grafana dashboards as JSON under
`monitoring/dashboards/`, and the component wraps each in a ConfigMap
with the `grafana_dashboard: "1"` label, which the kube-prometheus-stack
sidecar loads.

* **liken fleet.** Machines by phase, release per node and the skew
  from the target, changes pending by tier, approvals outstanding,
  upgrade durations, crashes, devices per class per node.
* **liken operators.** Layer 2 for every operator in the cluster:
  reconcile rate, error rate, duration, and watch restarts, by
  component and kind. One dashboard reads every repository because
  the names are the same.

### liken's own metrics

| Component | Metric | Type | Why |
| --- | --- | --- | --- |
| machine-operator | `liken_release_info{version, slot}` | gauge, info | release per node; fleet skew |
| machine-operator | `liken_machine_boot_timestamp_seconds` | gauge | uptime; reboot count over time |
| machine-operator | `liken_machine_change_pending{tier}` | gauge | `restart`, `reboot`; waiting on approval. An in-place change applies on the pass that finds it, so it never waits |
| machine-operator | `liken_machine_converged` | gauge | spec matches what runs |
| machine-operator | `liken_release_download_bytes_total` | counter | staging progress |
| machine-operator | `liken_release_download_failures_total` | counter | a download that keeps failing |
| machine-operator | `liken_upgrade_duration_seconds` | histogram | staged to booted on the new slot. Waits for a staging time that survives the reboot; the system release record carries none today |
| machine-operator | `liken_devices{class}` | gauge | a missing GPU or adapter shows as a drop |
| machine-operator | `liken_last_crash_timestamp_seconds` | gauge | pstore capture; a crash loop is a line |
| machine-operator | `liken_firmware_info{version}` | gauge, info | BIOS version. Waits for [plan 33](33-firmware-updates.md); status carries no firmware version today |
| machine-operator | `liken_firmware_update_pending` | gauge | milestone 30 state. Waits for plan 33 with the row above |
| cluster-operator | `liken_machines{phase}` | gauge | fleet by phase |
| cluster-operator | `liken_disruption_approvals_pending` | gauge | approvals outstanding |
| cluster-operator | `liken_machines_behind_target` | gauge | nodes not yet on the target release |

Most of these are Machine status fields. That is the point: the metric
is the status field over time. Console parity says what init prints
must reach Machine status, and this extends it one step, to Prometheus.
The metric reads the same fact the status writer reads, and never a
second source.

## Considered and set aside

**kube-state-metrics on the CRDs.** Its custom resource state feature
can turn status fields into metrics with no code. It would cover the
gauges and none of the counters or histograms, it names the series in
its own vocabulary, and it puts a second reader between the fact and
the graph. We own the names.

**A shared Go module.** Layers 1 and 2 are about fifty lines in each
repository. The operators import nothing from each other today, and a
module that exists only to hold those lines is a junk drawer. The
contract is the shared thing, and this document is the contract.

**Traces.** Not yet. Metrics first, and nothing here blocks tracing
later.

**Serving the metrics on the API server through an aggregated API.**
More moving parts than a port, and every scraper already speaks HTTP to
a pod.

## Proof

Each repository writes the failing test first: a real registry, a
scrape through the handler, and the expected series. Repeated scrapes
leave counters unchanged and make no hardware or API calls.

On liken-1, with a Prometheus that has the component applied, the
fleet dashboard shows every node's release and phase, and an upgrade
draws one bar in the duration histogram. Record the release and the
scrape interval when the drill runs.
