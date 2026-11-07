# 19, An adapter's entry is current while its pod is Ready

Proposed and built on 2026-11-07, and tested against `cectest` and the
fake API server. Revised the same day after a review found that every
start of a node workload read as stale, that the node workload
compared its entry with its own last write and not with the stored
entry, and that the probes failed a pod on a pass that still worked.
Replaces the heartbeat of [plan 09](09-cec.md). No drill on hardware
has run: the drills below have not run on the `lab` fleet or on a
physical test cluster.

## The problem

The node workload wrote its adapter's entry under the `CECBus`'s
`status.adapters` every 30 seconds, whether or not the entry changed.
The `Deployment` treated an entry older than 90 seconds as stale, and
ran a pass every 30 seconds to find such an entry, because a pod that
dies without its `Stopped` entry writes nothing that wakes the loop.
On a home cluster, each `CECBus` took 20 writes in 10 minutes, and the
only field that changed was `reportedAt`. Each write woke every
watcher of the `CECBus`es: the `Deployment` and each node workload.

The heartbeat existed only to catch a pod that stops without a word.
The kubelet and the node controller already report that fact on the
pod, so the operator can read it there.

## The design

1. The node workload writes its entry only when the entry differs
   from the one the bus stores, or the bus stores none. It compares
   with the API server's copy, apart from `reportedAt`, so an entry
   that a restore or another writer removed or changed is written
   again on a quiet bus. `reportedAt` stays in the schema, and is the
   time of the last write.
2. The `Deployment` and each node workload watch the pods of the node
   workload, in the operator's namespace, by the label
   `app=equipment-operator-cec`, through client-go's reflector
   (`cecpods.go`). An entry that is not `Stopped` is current while a
   pod on the entry's machine (`spec.nodeName`) is `Ready`. When no
   pod there is `Ready`, the entry is stale if the latest change of a
   pod's `Ready` condition, its `lastTransitionTime`, is later than
   the entry's `reportedAt`: the pod lost readiness after the write.
   An earlier change means the pod has not become `Ready` since the
   write, as a new pod is when its first entry lands, and the entry
   stays current for `cecStartupGrace`, 60 seconds, after the write.
   A pod that is still not `Ready` then, such as one that hangs at its
   start, makes the entry stale. The kubelet stamps the condition and
   the node workload stamps the entry with the clock of the same node,
   so the comparison has no skew between machines. Both stamps have
   one-second precision, and a pod that loses readiness in the same
   second as the write counts as starting, so its entry goes stale at
   the end of the grace. An entry with no pod on its machine is stale
   at once. The verdict keeps the status `Unknown` and the reason
   `Stale`, and its message says which case the pods showed. A
   `Stopped` entry wins over what the pods show, because it is the
   node workload's own last word.
3. The `Deployment`'s clock pass is gone. A pod event wakes the loop,
   so a change of readiness needs no clock. Two clocks remain, and
   each is armed only when it has work: a read or a write that fails
   arms a retry after 30 seconds, and an entry in a starting pod's
   grace arms a deadline at the end of the grace. The node workload
   arms the same deadline for another adapter's entry, because that
   adapter loses its turn to send the bus's commands then.
4. The node workload serves `/healthz` on port 9200, and the
   `DaemonSet` gives it a readiness probe and a liveness probe. The
   handler sends a question into the loop's `select`, and waits two
   seconds for the answer (`cecnode_probe.go`). A loop that is alive
   and blocked stops being `Ready`, and its container restarts. The
   readiness probe fails a pod after 3 misses 20 seconds apart, about
   60 seconds, because a pass that works can block the loop for one
   30-second request to the API server and a join scan. The liveness
   probe restarts it after 5 misses 30 seconds apart, about 150
   seconds. The readiness window is also the startup grace, and a
   test keeps the two equal.
5. A `Role` grants both identities `get`, `list`, and `watch` on pods
   in the namespace.

The node workloads also read the pods, which the request for this
change did not name. A node workload decides which adapter of a bus
sends the bus's commands, and which one speaks for a `Display`, from
the other adapters' current entries (`commands` in
`cecnode_television.go`, `speaksFor` and `othersOf` in
`cecnode_wake.go`). Under the heartbeat, an adapter whose pod died
lost its turn after 90 seconds. With no heartbeat and no read of the
pods, its entry would stay current for good, and the next adapter
would never take the commands. With an age rule and no heartbeat,
every entry would go stale after 90 seconds, and every adapter would
send the commands. So both workloads apply the same rule from the
same pods. When a node workload cannot read the pods, it judges no
other entry stale, logs that it does so, and tries again, so it can
wait for an adapter that is gone until the read works. The
`Deployment` writes no status while it cannot read the pods, and a
refusal names the `Role` that grants the read.

The first build of this plan counted any pod that was not `Ready` as
stale. A node workload writes its first entry before its pod is
`Ready`, so each start posted a Warning `Stale`, removed the
`Television`'s and the `Receiver`'s CEC facts for a moment, and let a
second adapter take the bus's commands while the first still sent
them. The grace and the comparison with `lastTransitionTime` answer
that.

## Timing

The heartbeat found a dead pod between 90 and 120 seconds after its
last write: 90 seconds of age, and up to 30 seconds more for the next
tick of the clock pass. The pods give these times:

* A container that exits: the kubelet reports the pod not `Ready`
  when the container ends, and the watch delivers it at once. A
  container that exits cleanly writes `Stopped` first.
* A loop that is alive and blocked: the readiness probe fails 3 times,
  20 seconds apart, so the entry is stale after about 60 seconds. The
  liveness probe restarts the container after 5 failures, 30 seconds
  apart.
* A new pod that never becomes `Ready`: its entry is stale 60 seconds
  after its first write, at the deadline the `Deployment` arms.
* A node that stops: the node controller marks the node's pods not
  `Ready` after its grace period, `--node-monitor-grace-period` of
  `kube-controller-manager`, and the watch delivers the change at
  once.

## Set aside

* **A `Lease` for each node workload.** It renews on a timer, so it
  writes as often as the heartbeat did, only to another object.
* **The `Deployment` writes a stale mark into the entry.** The entry
  belongs to the node workload's field manager. A second writer of
  the same entry adds a write for each change, and the node
  workloads still need the rule.

## Proof

The tests prove that an idle node workload writes no entry over ten
minutes of the bubble's clock, that an entry removed or changed behind
the node workload is written again with no change on the wire, that
an entry whose pod is `Ready` is current and becomes stale when the
pod stops being `Ready` after the write or is deleted, and current
again when the pod is `Ready`, with one `Event` for each change of
reason, that a starting pod's fresh entry is current and posts no
`Stale`, and becomes stale at the end of the grace with no event,
that a `Stopped` entry wins over a `Ready` pod, that an idle
`Deployment` loop sends the API server nothing over ten minutes, that
a refused status write is tried again after 30 seconds, that a
refused pod list names the `Role`, that the probe answers while the
loop runs and fails while an entry write blocks it, that the next
adapter waits while the first adapter's pod starts and takes the
commands when the grace ends, and that the next adapter takes the
commands when the first adapter's pod stops being `Ready`.

The drills that have not run:

1. On a fleet with one adapter, delete the node workload's pod with
   `--grace-period=0 --force`, and read the `CECBus`'s conditions and
   `Event`s: `Stale` while no pod runs, and no `Stale` while the new
   pod starts and becomes `Ready`.
2. Power off a machine that carries an adapter, and time the `Stale`
   condition against the node controller's grace period.
3. Read the `CECBus`'s `resourceVersion` over ten minutes on a quiet
   bus, and confirm that it does not move.
