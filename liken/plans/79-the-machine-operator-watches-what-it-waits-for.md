# 79. The machine operator watches what it waits for

Milestone 79. Proposed 2026-10-09. The fourth of five milestones that
remove the ten-second ticker from `machine-operator`'s reconcile loop.
[Milestone 76](completed/76-a-pass-reports-what-it-did-not-finish.md) gives the
series and the table of every job the ticker does. Three features of
`machine-operator` wait for objects that other programs change: the
proof of an upgrade's images, the drain before a reboot, and the
removal of a cluster feature. Each one lists those objects on every
pass while it waits, and the ticker starts the passes. This milestone
gives each wait a watch that runs only while the wait runs.

## What happens now

**The image proof.** After an upgrade, init imports the new release's
images and stages a record of the import. `settleImportsLifecycle` in
[`imports.go`](../machine-operator/imports.go) promotes the record only
when every OS container on the node, every container that runs a
`liken.sh/` image, is Ready. A torn image fails the kubelet's
readiness the same way a crash loop does, so the proof holds back the
promotion until each one runs. While a staged trial matches this boot,
each pass calls `kubernetes.ListPodsOnNode`, a direct list of every
pod on the node. The operator watches only its own pod, so when the
last OS container becomes Ready, the promotion waits for the next
tick.

**The drain.** Before a reboot, `gateThroughDrain` in
[`drain.go`](../machine-operator/drain.go) cordons the node, evicts its
pods, and waits for them to leave. While a granted reboot waits on the
drain, each pass lists the node's pods with the same call. When a pod
leaves, the drain sees it on the next tick. When a
`PodDisruptionBudget` refuses an eviction, the comment says "the next
pass asks again", and the next pass is a tick. The drain reboots
anyway 5 minutes after the time in the `liken.sh/draining-since`
annotation (`drainDeadline`), and a tick finds that the deadline
passed. The check is `now.Sub(since) > drainDeadline`, so a pass at
exactly the deadline still holds.

**The feature removal.** When a `Cluster` edit removes a feature,
`convergeClusterDocument` can hold the removal until no `HelmChart`s,
or no `LoadBalancer` `Service`s, remain in the cluster, because only
the feature's own controller can delete them
([`retraction.go`](../machine-operator/retraction.go)). Each pass
lists them across the whole cluster, and a removal can wait for hours
while a person deletes them.

## The design

**A watch for each wait.** Each wait starts a watch when it begins
and stops the watch when it ends. A wait begins and ends on a pass,
and each pass computes from the state it reads whether the wait
continues, so a restarted operator starts the watches it needs on its
first pass. The `operators` skill describes how to stop a watch and
start a new one (`followPeripherals` in
`bluetooth-operator/editwatch.go`). The watch's first list costs the
same as one list today, and its `Synced` callback wakes the pass that
reads it.

| Wait | Watch | Selector |
| --- | --- | --- |
| The image proof | pods | `spec.nodeName=<node>`, every namespace |
| The drain | pods, and `PodDisruptionBudget`s | pods as above; every budget in the cluster |
| The feature removal | `HelmChart`s, or `Service`s | every object of the kind that the precondition names |

The proof and the drain share one pod watch while either runs. A
watch that runs only during a wait does not wake a pass for every pod
write on a busy node the rest of the time, and its copy costs memory
only during the wait.

**The operator's own pod.** The narrow watch on the operator's own pod
stays as it is. `decidePodStale` returns on the first pod in its list
that carries `liken.sh/os-version`, and that is correct only because
the watch selects one pod. `liken-cluster-operator`, `liken-iscsid`,
and `machine-logs` carry the same annotation, and their names sort
before `liken-machine-operator`. So the node-wide watch does not
replace the narrow one.

**The fields the copy keeps.** The node-wide pod copy uses a
`Transform` (`kubernetes/informer`) that keeps only what the proof and
the drain read: the name, the namespace, the UID, the
`resourceVersion`, the owner references, the deletion timestamp, the
two annotation keys the code reads (`kubernetes.io/config.mirror` and
`liken.sh/os-version`), `status.phase`, each container status's name,
image, and readiness, `spec.volumes[].hostPath.path`, and
`spec.resourceClaims`. A field the trim drops converts to its zero
value with no error. Without the owner references, every `DaemonSet`
pod looks evictable, and without the mirror annotation, the drain
evicts mirror pods that the kubelet creates again. A test converts a
full pod with and without the `Transform` and requires the same
`kubernetes.Pod`.

**When a copy is not ready.** A read falls back to the API server when
its copy is not ready (`watches.go`), and the proof and the drain do
the same. A failed fallback still holds the drain, as the comment in
`disruptions.go` requires: "A Node that reads but whose pods do not list
holds the reboot."

**The drain deadline.** Every pass that holds for the drain asks
milestone 76's recorder for a wake at `draining-since` plus 5 minutes.
`decideDrainStep` returns the deadline, because the pass that cordons
sets `since` to its own start. The check becomes `>=`, which is what
Cluster API uses for its drain timeout, so the pass that the wake
starts at the deadline lets the reboot proceed. The annotation holds
whole seconds, so the deadline is computed from the parsed
annotation, not from the time in memory.

**A refused eviction.** The drain sorts the eviction answers as
`kubectl drain` does (`k8s.io/kubectl/pkg/drain`). A `404` means the
pod is gone, which happens when the copy is a moment behind. A `429`
means a budget refused it: when it carries `Retry-After`, the pass
asks for a wake at that time, and otherwise the budget watch wakes
the pass when a budget's `status.disruptionsAllowed` rises. A `403`,
a `5xx`, or a network error is a failure for milestone 76's retry. The
budget watch needs `list` and `watch` on `poddisruptionbudgets`, which
the RBAC does not grant today.

For comparison, `kubectl drain` waits 5 seconds after a refused
eviction (`EvictErrorRetryDelay`), Cluster API requeues every 20
seconds, and Karpenter backs off per pod from 100 milliseconds to 10
seconds. Each of them polls. The budget watch wakes the pass when a
budget changes, which is sooner than any of them in most cases.

**The comments that change.** The watch list at the head of
`watches.go`, the RBAC comment that says the watch is of the node's
own operator pod, the `drainOrder` comment that says each pass lists
the node again, and the eviction comment in `drain.go`. The pass
fixtures in `pass_test.go` serve the list path and also need the
watch path.

## The memory to measure

A pod object without `managedFields` costs an estimated 10 to 40 KB in
memory, and a node can run up to 110 pods, so a full copy costs a few
megabytes during a wait. A trimmed pod is under 1 KB. Measure the
operator's memory on a one-gigabyte screen machine during a drain,
with and without the trim. The kubelet watches its pods with the same
selector, and the API server's watch cache indexes pods by
`spec.nodeName`, so a watch per node adds little load to the server.

## Tests

In a `synctest` bubble with `kubernetes/apiservertest`:

- An image proof with one OS container not Ready promotes the record
  when the fake API server marks the container Ready, with no tick.
- A drain sees an evicted pod leave with no tick, and the pod watch
  stops when the drain ends.
- A drain with a pod that never leaves reboots when the fake clock
  reaches the deadline. An operator that starts during a drain asks
  for the wake from the annotation.
- A refused eviction is asked again when the fake budget's
  `disruptionsAllowed` rises, and at its `Retry-After`.
- A node that runs a `liken-cluster-operator` pod still reads its own
  pod's staleness from the operator's pod.
- A held feature removal finishes when the last `HelmChart` is
  deleted, and its watch stops.
