# 88. Disruptions and machine disruption budgets

Milestone 88. Proposed 2026-10-21. Release 1 of stage 1 was built
and drilled on the `lab` fleet on 2026-10-21
([Release 1 as built](#release-1-as-built)). Release 2 and stage 2 are
not built, and the drills on liken-1 and stick-1 have not run. Five
reviews from different angles read an earlier draft on the same day,
and this version answers them
([Considered and set aside](#considered-and-set-aside)).

Every time a machine must go out of service, a `Disruption` records
it, and `kubectl get disruptions` shows who asked, why, and whether it
waits for approval, waits for room in a budget, is granted, or is
done. A release, a spec change, a person, and later `firmware-operator`
all ask the same way. A person sets the limits with
`MachineDisruptionBudget`s, the `PodDisruptionBudget` of machines: one
named `default` covers the fleet, and stricter ones cover a group of
machines. The rule of one leader at a time applies on top of every
budget. When a machine takes its turn, everything pending for it
shares one drain and one reboot, including a request that arrives
while the machine is still cordoned. A `Manual` machine's person
approves one `Disruption`, and that approval cannot carry over to a
later request. Turns are still granted through the ledger in
`Cluster` status that [milestone 82](completed/82-the-cluster-records-each-reboot-turn.md)
built, so the budget stays one compare-and-swap. The move takes two
releases, and each one works beside machines on the release before
it.

The work has two stages. This milestone's first stage moves every
reboot and restart that `liken` makes today onto `Disruption`s. The
second opens `Disruption`s to other components: `firmware-operator`
writes firmware inside a turn
([its plan 06](../../firmware-operator/plans/06-applying-a-database-update.md)),
and [milestone 34](34-gpu-add-ons.md)'s GPU add-ons can do the same.
[Milestone 33](33-firmware-updates.md) holds the rest of the OS's part
of firmware updates.

## The problem

One reboot turn today crosses three components:

| Part | Where it lives |
|---|---|
| A machine asks for a turn | machine-operator sets the reason `AwaitingTurn` on any convergence condition (`gateDisruption` in `machine-operator/converge.go`). A person asks with the `liken.sh/request-reboot` annotation (`machine-operator/rebootrequest.go`), and approves a `Manual` machine with `liken.sh/approve-disruption`. |
| The fleet grants it | cluster-operator's conductor (`cluster-operator/rollout.go`) reads `AwaitingTurn` with `wantsTurn`, records the machine in `Cluster` `status.rebootTurns`, and then writes a `RebootApproved` condition onto the `Machine`'s status, conditional on the `Machine`'s `resourceVersion`. The budget is `Cluster` `spec.disruption.maxUnavailable`, and the floor of one leader at a time is a rule in `decideRollout`. |
| The machine carries it out | machine-operator cordons and drains its node (`machine-operator/drain.go`), writes a `RebootIntent` or a `RestartIntent` for `init` (`machine/reboot.go`), and uncordons when nothing is pending. A restart takes a turn and drains nothing, because pods survive a k3s restart. |

This has four costs:

- **Only machine-operator can ask.** An add-on that changes a machine
  below Kubernetes needs a drained machine and a reboot that the
  conductor orders, and it has no way to ask for one. The request
  annotation reboots in the same pass as the drain, and the drain
  evicts any pod that is not a DaemonSet's, so an add-on has no step
  in which to work. Writing the annotation also needs `patch` on
  `machines`, which lets a component change any machine's spec.
- **Approvals can be reused.** The open problem
  [disruption-approvals-can-be-reused](open-problems/disruption-approvals-can-be-reused.md)
  shows how an approval of one configuration hash permits a later
  disruption to the same bytes that nobody approved.
- **A person cannot see the requests.** What a machine waits for is a
  reason on one of its conditions, and its grant is a condition that
  another program writes. Nothing lists the requests of the fleet.
- **Requests from different sources do not share a drain.**
  machine-operator already passes every staged document through one
  gate per pass, so its own changes share one drain and one reboot. A
  release that is still downloading when a spec change takes its turn
  takes a second turn and a second drain. An add-on's request would
  take its own.

## `Disruption`

One `Disruption` is one request to take one machine out of service.
It is cluster-scoped, in `liken.sh`, beside `Machine`:

```yaml
apiVersion: liken.sh/v1alpha1
kind: Disruption
metadata:
  name: node-3.machine-operator
spec:
  machine: node-3
  requester: liken.sh/machine-operator
  kind: Reboot
  reason: apply release 2026.10.21-001 and the staged spec
  hash: 6f2a91c04b7e...
  approvedHash: ""
status:
  conditions:
    - type: Approved
      status: "True"
      reason: AutoPolicy
    - type: Granted
      status: "False"
      reason: AwaitingTurn
      message: the default budget has no room; node-4 holds a turn
```

```
$ kubectl get disruptions
NAME                       MACHINE   REQUESTER                   KIND     APPROVED   GRANTED   AGE
node-3.machine-operator    node-3    liken.sh/machine-operator   Reboot   True       False     40s
node-5.reboot-0c1e9a2b     node-5    kubernetes-admin            Reboot   True       True      5s
```

- **`spec.machine`** names the machine. The CRD declares it in
  `selectableFields`, so each machine-operator watches only its own
  machine's `Disruption`s.
- **`spec.requester`** names who asked, as a key with a domain prefix
  for a component, and the Kubernetes user name for a person. It does not
  change. machine-operator finds its own request by it, and a person
  reads it.
- **`spec.kind`** is `Reboot` or `Restart`. A reboot covers a
  restart, so a machine with both pending takes only the reboot.
- **`spec.reason`** says why, for the person who reads it.
- **`spec.hash`** names what the request applies: the staged
  documents' hash for machine-operator, and the boot it was filed
  against for a person's reboot.
- **`spec.approvedHash`** is a `Manual` machine's approval. `liken
  approve-reboot` sets it to `spec.hash`. A request is approved when
  its machine's `rebootPolicy` is `Auto`, or when the two hashes are
  equal. When the staged documents change, `spec.hash` changes, and
  the old approval no longer matches.
- **`status.conditions`** are `Approved`, `Granted`, and `Complete`.
  The conductor writes the first two for display. A condition on the
  `Disruption` is never a grant; [The turn](#the-turn) says what is.

machine-operator keeps one `Disruption` for its machine, named
`<machine>.machine-operator`. It creates it when any staged document
needs a disruption: the spec, the cluster document, the system
release, the registry credentials, or the demotion. When more is
staged before the turn, it updates `kind`, `reason`, and `hash`. When
nothing is staged any more, it deletes the request. So the request
completes by deletion, and its approval goes with it. A later return
to the same bytes makes a new request that nobody approved, which
closes the open problem above.

A person's `liken request-reboot` creates `<machine>.reboot-<boot>`
with `kind: Reboot` and the running boot in `spec.hash`. machine-operator
sets `Complete` when the machine reports another boot, the same
construction that makes the request annotation one-shot today.
cluster-operator's janitor deletes a `Complete` `Disruption` a day
after it completes. The `Event`s of each transition carry the history
for the hour that the API server keeps them.

## `MachineDisruptionBudget`

A `MachineDisruptionBudget` limits how many of the machines it selects
may be out of service at once:

```yaml
apiVersion: liken.sh/v1alpha1
kind: MachineDisruptionBudget
metadata:
  name: gpus
spec:
  selector:
    matchLabels:
      pool: gpu
  maxUnavailable: 1
```

```
$ kubectl get machinedisruptionbudgets
NAME      MAX   SELECTED   OUT   ALLOWED   AGE
default   2     5          1     1         90d
gpus      1     2          1     0         3d
```

- **The selector** reads the `Machine`'s labels. An empty selector
  selects every machine, as in a `PodDisruptionBudget`.
- **Several budgets can select one machine**, unlike pod budgets,
  where the eviction API refuses a pod that two budgets select. The
  machine takes a turn only when every budget that selects it has room.
- **`default` always exists.** It selects every machine. The conductor
  creates it from `spec.disruption.maxUnavailable` when it is missing,
  and never deletes it, so a machine that no other budget selects is
  still limited.
- **The leader floor stays in code.** One leader at a time, from
  `Cluster` `spec.leaders`, applies whatever the budgets say. A
  `Machine` has no role in its spec, so a budget for leaders would need
  a label that an operator keeps in sync, and a rule against loosening
  it, for the same five lines.
- **Out of service** counts the machines that hold a turn and the
  selected machines that are unavailable without one: `Lost`,
  `Degraded`, or rebooting without a turn. A budget with no room says
  so in its `DisruptionAllowed` condition, and each waiting
  `Disruption`'s `Granted` message names the budget that refuses it.

## The turn

A turn is an entry in `Cluster` `status.rebootTurns`, as in milestone
82. The entry gains the names of the `Disruption`s it grants:

```yaml
status:
  rebootTurns:
    - machine: node-4
      since: "2026-10-21T18:02:11Z"
      disruptions: [node-4.machine-operator, node-4.reboot-77b0c1d9]
```

The conductor adds an entry, and adds a `Disruption` to an entry, only
by a write of the `Cluster`'s status conditional on its
`resourceVersion`. So every grant is one compare-and-swap on one
object, a paused former leader's late write gets `409 Conflict`, and
the budgets and the leader floor are checked in that same write.

**The conductor** grants a turn to a machine with an approved
`Disruption` when every budget that selects it has room, in the order
it uses today: leaders first while the release lags on them, then
name order. It does not grant a turn to a machine whose release is
still downloading, so the release joins the same drain. The rule
holds for every requester, a person included. machine-operator
reports a fetch that failed and waits to retry with the reason
`DownloadRetrying`, in the phase `Downloading`, and the conductor holds
a turn only for the reason `Downloading`. So a release server that is
down holds no request. While a machine holds its entry, the conductor
adds each new
approved `Disruption` for that machine to the entry. The conductor
removes the entry when the machine is available and every
`Disruption` the entry names is complete or deleted.

**machine-operator** acts only on its machine's entry. Before it
starts a drain and before it writes an intent, it reads the `Cluster`
directly, not from its watch's copy, and finds its machine in
`status.rebootTurns`. It applies every `Disruption` the entry names in
one step: a drain and a reboot when any of them is a reboot, or a
restart with no drain. A `Disruption` that arrives after the intent is
written waits for the boot after it. When its machine has no entry and
nothing is pending, it uncordons, as `decideUncordon` does today.

A machine that does not come back holds its entry, and the rollout
stalls as it does today: `Progressing` on the `Cluster` turns `False`
with the reason `RolloutStalled`, and no other machine gets a turn. A
`Machine` that is deleted leaves the ledger by the rule milestone 82
already has.

## Stage 2: other requesters

The second stage opens `Disruption`s to other components, after the
first stage has run on the fleet. It adds fields only when a
requester needs them:

- **`spec.hold`** stops the turn after the drain. The requester's pod,
  which tolerates the cordon, does its work on the drained machine,
  then clears `hold`, and the step reboots. A hold has a deadline, and
  a request whose hold expires leaves the reboot with a `Warning`
  `Event`. `firmware-operator`'s plan 06 needs this.
- **`spec.exclusive`** keeps a request out of a shared reboot. Under
  the same cordon, each exclusive request takes its own reboot, oldest
  first, and the shared requests take the last one, so a release boots
  on the firmware the steps before it installed. A capsule is
  exclusive, under milestone 33's rule of one firmware change per
  reboot. A failed step holds the rest of the turn until a person
  clears it. `firmware-operator`'s plan 08 needs this.
- **A request that can wait.** A `dbx` update has no urgency, and it
  can join the next turn that its machine takes for a release. The
  requester can do this on its own: it watches the machine's
  `Disruption`s and files its request when another one is pending, or
  when its own deadline passes.
- **Approval out of the requester's reach.** A component that can
  create a `Disruption` must not be able to set `spec.approvedHash` on
  a `Manual` machine. A `ValidatingAdmissionPolicy` limits that field
  to people.

RBAC lets a component create `Disruption`s and nothing on `Machine`.
A turn still waits for the budgets and for `rebootPolicy`, so a
component that can create a `Disruption` cannot reboot a machine on
its own.

## Two releases

The fleet runs mixed releases during every upgrade, and the upgrade to
this milestone is itself a rollout through the old turns. Three facts
decide the order:

- machine-operator's binary on each node is that node's release, from
  the node-local `installed` tag. Its pod template, its RBAC, and the
  CRDs come from the AddOn manifests that a leader's boot writes
  ([system-pods-lag-the-booted-release](open-problems/system-pods-lag-the-booted-release.md)).
  So a follower can run the new binary before the cluster has the new
  CRDs or the RBAC to read them, and a rollback can leave a new binary
  with the old RBAC.
- cluster-operator is one `Deployment` on the `installed` tag, so its
  binary is the release of whichever node runs it, and it can move
  between releases in the middle of a rollout.
- An older CRD revision prunes the fields it does not know, and
  `init` keeps the newer CRD on disk after a rollback.

**Release 1** adds both CRDs, the RBAC, the `default` budget, and
`disruptions` in the ledger's entries. The grant stays milestone 82's:
the entry, then `RebootApproved` on the `Machine`, and machine-operator
acts on `RebootApproved` alone. machine-operator files its
`Disruption` and still sets `AwaitingTurn`, so an older conductor sees
the request. The conductor still grants a turn on `AwaitingTurn`, and
the entry names the machine's approved `Disruption`s. A conductor that
granted on a `Disruption` alone would grant a machine-operator of the
release before, which ignores a `Disruption`, and that grant would hold
a slot of the budget with no reboot to spend it. machine-operator
treats a person's `Disruption` as the request annotation, and its
`approvedHash` as the approve annotation. For its own documents it
still gates on the approve annotation, because one annotation approves
one document and `spec.hash` covers them all. The CLI writes each
`Disruption` and the annotation with it, so a machine on either
release acts on the request. Both binaries treat a `403` or a
`404` on the new resources as "the old protocol only". The conductor
also stops dropping a ledger entry whose machine changed without a
`RebootApproved`, when the entry names a `Disruption`, so release 2's
grant by entry alone is safe beside it.

**Release 2** takes release 1 as its rollback floor. machine-operator
acts on its ledger entry alone, and on `approvedHash` for its own
`Disruption`. The conductor grants on a `Disruption`, and still writes
`RebootApproved`, because a machine-operator of release 1 acts on
nothing else. The two annotations, `AwaitingTurn` as a request, and
`spec.disruption` as the source of the budget are removed from the
code. `RebootApproved`, and milestone 82's rules for confirming it on
the `Machine`, are removed when the floor passes release 2. The fields
stay in the CRDs, unread, until then, so a rollback finds what it
reads.

## The manual

The manual changes in the same commits. The guides `troubleshoot`,
`upgrade`, `rollback`, `remove-nodes`, `add-nodes`, and
`hardware-modules` teach the turn, the request annotation, or
`approve-reboot`, and each changes to `Disruption`s. The concept page
`how-liken-works` explains the turn. The reference gains pages for
both resources, and `cli`, `cluster`, `machine`, and `metrics` change.
The skills generated from those guides change with them.

## Proof

Each release runs on the `lab` fleet first: an upgrade from the
release before it, with the conductor's pod deleted in the middle so
that it moves to a node of the other release, and a rollback. Then on
liken-1 and stick-1. A cluster that people depend on upgrades last,
one release at a time.

The drills:

- A machine with a staged spec and a release that is still
  downloading takes one drain and one reboot for both.
- A person's `Disruption` that arrives during a turn joins it, with no
  second drain.
- A cluster-document change restarts k3s with no drain.
- A `Manual` machine waits for `approvedHash`. An approval of one hash
  does not approve the next, and a return to approved bytes asks
  again.
- A machine that the `gpus` and `default` budgets select takes a turn
  only when both have room, and a worker that only `default` selects
  is still limited.
- A conductor paused with `SIGSTOP` after its leadership check, a new
  leader that grants another machine, then the old pod resumed: its
  write gets `409`, and the budget holds.
- Release 1 beside release 0, and release 2 beside release 1, in both
  directions.

## Release 1 as built

Release 1 follows [Two releases](#two-releases), with three additions
that the build found it needed:

- **A person's approval of machine-operator's request.** The request
  covers every staged document, and machine-operator still approves
  one document at a time through the approve annotation. So
  machine-operator reads a request whose `approvedHash` names its
  hash as an approval of the change that `liken approve-reboot`
  picks: the first reboot-class change, or else the first
  restart-class one. Without it, `kubectl patch` of `approvedHash`
  showed `Approved` and moved nothing.
- **An owner for a request made with `kubectl`.** machine-operator
  names the `Machine` as the owner of a person's request that has
  none, in the same write that names the running boot, so the request
  goes with its `Machine`.
- **The conductor's view of a kind it does not serve.** The shared
  informer now reports a collection that the API server refuses as
  absent, and the shared client reports a `403` as `ErrForbidden`.
  The conductor creates the `default` budget only when the kind is
  served, and both operators read a refused kind as no requests.

The approve annotation still approves a return to bytes it approved
before. Release 2 removes the annotation, and with it that path.

The drills ran on the five QEMU guests of the `lab` fleet, with
`2026.10.21-901` built from the commit before this work and
`2026.10.21-902` and `-903` built from it. A monitor read the fleet
every five seconds, and every drill below kept the budget: no sample
showed more machines out of service, or more ledger entries, than the
limit, and none showed two leaders out.

- `-901` to `-902` with the conductor's pod deleted mid-rollout, so it
  moved from a `-901` node to a `-902` node. The new CRDs arrived with
  the first leader's boot, and the conductor created `default`. Five
  turns, one at a time.
- A person's request through `liken request-reboot`: approved,
  granted, rebooted, and `Complete` in under a minute.
- A request created with `kubectl`, with no hash, during another
  request's 60-second drain: it joined the entry, and one drain and
  one reboot completed both.
- A Cluster change that k3s applies with a restart: five `Restart`
  requests, one turn at a time, no cordon, and no machine rebooted.
- A `Manual` machine: its request read `Approved False` and waited.
  An `approvedHash` alone, in the short form, released it. A return
  to the same staged bytes made a new request that waited again.
- A `gpus` budget of one over `node-4` and `node-5`, with `default` at
  two: at most two out, never both `gpus` machines, never two leaders.
- `node-5` powered off during its drain: it held its entry, read
  `Lost`, and after ten minutes the rollout stalled. A new request
  waited with the stall in its `Granted` message. The machine
  returned, its request completed, and the rollout went on.
- `-902` to `-903` through a release server limited to 1.5 MB/s, with
  a reboot-class spec staged on `node-4` at the same time. `node-4`'s
  turn waited for the download, and one turn and one reboot applied
  both. Mid-rollout, a deleted request came back on the next pass,
  and a deleted conductor pod changed nothing.
- The release server down while `node-4` staged a spec: every machine
  read `DownloadRetrying`, posted one `Event` across several retries,
  and `node-4` took its turn for the spec alone.
- `-903` to `-902`, to `-901`, and forward to `-903` again. The CRDs,
  the budgets, and the old requests survived `-901`.
- Two conductors, with the leader stopped by `SIGSTOP` for 45 seconds
  and resumed, seven times through a rollout of five reboots. The
  Lease changed hands seven times.
- The `default` budget deleted: the conductor created it again from
  `spec.disruption`. machine-operator's pod deleted during a drain: the
  new pod finished the drain and the reboot.

Three of about 40 reboots hung in QEMU: the guest printed `reboot:
machine restart`, then nothing, with QEMU at 100 to 200% CPU, and a
power cycle booted it normally. A loop of requested reboots caught a
fourth with QMP. The boot CPU ran 64-bit code in a PE image that holds
the `BOOTSERV` and `DXE_SERV` table signatures, OVMF's DXE core, in a
small loop, and the other three CPUs were still at the reset vector.
So the guest hangs in the firmware's DXE phase after a warm reset,
before the boot manager starts systemd-boot, and no `liken` code runs
there. The variable store that hung boots on a power cycle, so nothing
`init` wrote to it is the trigger. The hang needs a busy host: one
guest took 60 reboots in a row with none, and three guests that
rebooted at the same time hung once in 38. The lab ran QEMU 10.2.1 and
OVMF 2025.11 on the laptop. The drills skill tells the next drill to
power-cycle a guest that hangs this way.

## Considered and set aside

- **One `Lease` for each unit of a budget.** A slot per `Lease` counts
  the holders correctly, but the grant and the count were then on
  different objects, which brought back milestone 82's race: a late
  grant on the `Disruption` landed after the slot was free. The count
  of unavailable machines also spanned objects, so two conductors
  could each grant against a different slot and exceed the budget.
  `PodDisruptionBudget`, kured, and FleetLock all keep their holders
  in one object for the same reason.
- **A turn object of its own.** A new singleton would take the ledger
  off `Cluster`, but the ledger is one status field, it is built and
  drilled, and moving it would need a third release.
- **A `phase` on the `Disruption`.** A list of phases repeats what the
  conditions and the ledger already hold, and the root rule is that a
  condition answers what is true now.
- **A budget for leaders.** Set aside above, under
  [`MachineDisruptionBudget`](#machinedisruptionbudget).
- **A limit on a turn's length.** A stream of requests could keep one
  machine cordoned. In the first stage, only a person and the
  machine's own documents make requests, so no limit is needed. The
  ledger's `since` gives the length if a later stage needs one.
- **Three releases.** An earlier draft kept the ledger in charge for
  one release and moved to `Lease`s in the next. With the ledger kept,
  release 1 already speaks both protocols, and two releases are
  enough.
- **The name `MachineDisruption`.** It pairs with
  `MachineDisruptionBudget`. `Disruption` is shorter, and a `Machine`
  is the only thing in `liken.sh` that it can disrupt.
