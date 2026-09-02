# A screen keeps its catalog

Plan 32. A screen's Corrosion agent takes a `PersistentVolumeClaim`
instead of an `emptyDir`, so a screen that restarts syncs a delta and
the media browser draws the wall at once. The `Catalog` sizes the
claim as it sizes every agent's and may name its `StorageClass`, and a
screen that cannot keep its claim on its node gets a new one. This changes what plans 06 and 15 built, and it answers
the screen half of the [ingest memory
problem](../open-problems/ingest-memory-and-restart.md).

## The problem

A screen pod holds a Corrosion agent on an `emptyDir`, so every start
of that pod is a first start. The agent joins the namespace's cluster
through the `catalog` `Service` and pulls the whole catalog before the
browser has rows to draw. The proof of concept measured that pull at
17 s for 105,000 rows on the workstation, and plan 06's drill measured
more than ten minutes on the lab's box, at about one core and 205 MiB.
Until it finishes, the screen shows an empty library list.

The pull also costs memory. A first full sync peaks at up to 380 MB
resident and the agent holds that for its whole life. The agent's
memory limit is 512Mi, so the peak fits, but it is paid on every pod
start: a roll of the operator, a template change, a kubelet restart, a
reboot of the machine.

Every other agent in this design already has the answer. A worker
`Job`'s agent runs on the `Library`'s own catalog claim, and the
catalog pod runs on the namespace's durable claim. Both keep their rows
and their actor id between runs, so both sync a delta. The screen is
the one agent left on an `emptyDir`.

A screen pod is pinned to one machine already. It draws through the
display claim `media-operator` allocated, and that claim places the pod
on the machine that holds the display. So a node-local class, such as
`local-path`, is the class a person is expected to name, and the pod
comes back to the same node and the same volume.

## The contract

- **Every screen gets a claim.** A screen's agent runs on a
  `PersistentVolumeClaim` sized from `Catalog.spec.storage.size`, the
  size every agent in the namespace takes, because a screen holds the
  same rows the durable catalog holds. There is no opt-in and no
  `emptyDir` left, except in a namespace with no single `Catalog`,
  below.
- **The `Catalog` may name the screens' class.** `Catalog.spec.screens`
  is a new block with one field, `storageClassName`. It is optional,
  and an absent value binds the cluster's default `StorageClass`, the
  way `spec.storage.storageClassName` does. A `liken` cluster ships
  `local-path` as that default, and a node-local class is the right one
  for a screen.

  ```yaml
  spec:
    storage:
      size: 1Gi
    screens:
      storageClassName: local-path
  ```

- **The class is a field of its own, not `spec.storage`'s.**
  `spec.storage` classes the namespace's one durable copy, and losing
  that copy costs a full rescan of every volume. A screen holds a
  replica, and losing it costs one sync. Those two may want different
  classes: the durable catalog on whatever the cluster calls durable,
  and a screen on the machine the display is already on. The size is
  one value for both, by definition. The `screens` block is also where
  a later screen-wide setting goes.
- **The choice is namespace-wide.** Every screen in the namespace takes
  the same class and the same size. This operator adds no field to
  `Player`, because `media-operator` owns that resource. A class per
  screen needs a resource of this operator's own that binds a screen to
  what it shows, and that resource is undesigned. It is the [open
  problem on which libraries a screen
  shows](../open-problems/which-libraries-a-screen-shows.md).
- **The `Player` owns the claim.** The claim carries a controller
  `ownerReference` on the `Player`, as the screen pod does, so the
  garbage collector takes both when the `Player` goes. A screen's
  catalog has no value without the `Player` it draws for. A claim owned
  by the `Catalog` would outlive every `Player` it was made for, and
  nothing would collect it.
- **The claim's name is derived.** It is the screen pod's name and
  `-catalog`, so `studio-lg-media-browser-catalog` for the `Player`
  `studio-lg`. Every pass names the same claim and the operator keeps
  no record of what it created, which is the rule
  `scannerCatalogClaimName` and `catalogPodClaimName` already follow.
  The node is not in the name, because the operator cannot know the
  node until the scheduler has placed a pod that already names the
  claim.
- **The claim is provisioned once and never rewritten.** The pass reads
  it by name, creates it when there is none, and leaves an existing one
  alone. A claim's spec is immutable once it binds, so a later change
  to the class or the size reaches a new claim, not a bound one.
  Volume expansion is not built, here or anywhere else in this
  operator.
- **The claim stands before the pod.** `standCatalogPod` already
  creates its claim before it stands its pod, and the screen pass does
  the same. A pod that named a claim nothing had created would sit
  Pending until the next pass.
- **A namespace with no single `Catalog` keeps its `emptyDir`.** A
  screen stands today whether or not the namespace holds exactly one
  `Catalog`, and this plan does not change that. With no `Catalog`
  there is no size to read, so the pod holds an `emptyDir` and the
  screen works as it does now. This is the one `emptyDir` left.
- **The screen pod needs the `ReadWriteOnce` handoff.** The claim is
  `ReadWriteOnce`, because one agent writes one SQLite database. A
  screen pod rebuilt on a template change must release the claim before
  the replacement mounts it. `standPod` already waits: a pod carrying a
  `deletionTimestamp` counts as still present, and the create happens
  on the pass that finds no pod. The code is right and the comment
  above it is wrong. It reads "A screen pod's catalog is an `emptyDir`
  and needs no handoff", and this plan corrects it. The comment at the
  top of `screenpod.go` states the same fact and needs the same repair.
- **An unschedulable screen loses its claim.** A node-local volume
  binds to the node the pod first landed on, and the `PersistentVolume`
  carries that node in its `nodeAffinity`. When a `Player`'s display
  moves to another machine, the display claim places the screen pod
  there, no node satisfies both the display and the volume, and the pod
  stays Pending forever. The operator recovers it: when a screen pod
  carries `PodScheduled: False` and has carried it for longer than the
  unschedulable grace, and its catalog claim is `Bound`, the operator
  deletes the pod and the claim, and the next pass creates both. The
  new claim binds on the new node and the agent pays one full sync.
- **`Bound` is the loop guard.** The operator deletes only a claim that
  is `Bound`. A claim that never binds, because the class has no
  provisioner or the class does not exist, stays `Pending`, so the
  recovery fires once at most and then reports rather than repeats.
- **The delete is guarded by three checks.** The operator deletes a
  claim only when its name is the derived screen claim name, it carries
  the screen name label and the player label, and its controller
  `ownerReference` names the `Player` with the UID this pass read. A
  `Library`'s media claim matches none of the three.
- **The grace is measured from the cluster, not from memory.** The
  operator reads `lastTransitionTime` on the pod's `PodScheduled`
  condition, which the API server writes. So a restarted operator holds
  the same verdict as the one before it, and no pass keeps a timer.
  `PodCondition` in `objects.go` carries no such field today and gains
  one.
- **`Catalog.status` reports the screens.** A new
  `status.screens` list holds one entry per screen pod in the
  namespace: the `Player`, the claim, the node the pod runs on, and the
  pod's phase. The `Ready` condition does not change. It follows the
  catalog pod alone, because that pod holds the durable catalog, and a
  dark screen must not report the namespace's catalog as down.
- **RBAC gains one verb.** `persistentvolumeclaims` gains `delete`,
  beside the `get` and `create` it holds. The comment in
  `deploy/rbac.yaml` that says there is no delete because the
  `ownerReference` is the whole teardown changes to name this one
  delete and its three guards.
- **The restart trick goes.** Plan 06 designed a restart of a screen's
  sidecar after its first full sync, to return the ingest peak. It was
  never built, and plan 06's own drill records that the agent never
  restarts. This plan states that no screen sidecar is restarted. With
  a claim the peak is paid once per claim rather than once per pod
  start, and 512Mi already covers it. Plan 09's drill step, which asks
  for the memory "after the sidecar's restart", asks for the memory
  after the first sync instead.
- **A screen keeps its actor id.** A Corrosion agent's actor id lives
  in its database. An `emptyDir` screen is a new actor on every start,
  so the cluster's member list grows one actor per restart and carries
  the dead ones until it declares them down. On a claim, one screen is
  one actor for the life of the claim: the member list holds one entry
  per pod, and a restarted agent asks its peers for the versions after
  the ones it already holds rather than for all of them. That is the
  same property a worker `Job`'s claim gives, which plan 28 states.
- **Nothing else about a screen changes.** The pod keeps its member
  label, so it is still a peer in the namespace's `EndpointSlice` and
  still a bootstrap peer for the next screen. The browser keeps its
  mount of the catalog volume, which stays writable for SQLite's `-shm`
  file. The libraries are still mounted read-only. The pod still
  carries no `ServiceAccount` token.

## Failure

**The claim's node is down.** The screen pod does not move, because the
volume is on that node. The display is on that node as well, so the
screen is dark for a reason that has nothing to do with the catalog.
The pod recovers when the node returns, and it starts on the catalog
its claim holds.

**The claim is full.** The agent's writes to SQLite fail. The browser
keeps drawing the rows the database already holds, so the screen goes
stale rather than dark. The operator does not detect this: it reads no
volume usage, and `Catalog.status.screens` reports the claim as
`Bound`. The cure is to raise `spec.storage.size` and delete the
screen claims, because a bound claim's size cannot be changed.

**The class never binds.** A class with no provisioner, or a name that
matches no `StorageClass`, leaves the claim `Pending` and the pod
unschedulable. The operator deletes nothing, because it deletes only a
`Bound` claim, and `Catalog.status.screens` reports the pod Pending
with its claim. A screen that worked on an `emptyDir` goes dark until
the class is fixed. A cluster whose default class never binds has no
working screen in any case, because every agent in the namespace
takes a claim of that class.

**The `Player` is deleted.** The garbage collector removes the pod and
the claim, because both carry the `Player` as their controller. A
`Player` deleted and recreated under the same name has a new UID, so
the old claim goes and the new screen starts on a fresh one with one
full sync.

**The display moves while the node is healthy.** This is the
unschedulable case above. The screen is dark from the moment the pod is
Pending until the grace passes, the claim is deleted, and the new
agent's first sync completes.

## The change on a running cluster

The operator rolls, and every screen pod in every namespace with a
`Catalog` is rebuilt once. The screen pod's template names a claim in
place of the `emptyDir`, so its hash differs, the operator deletes the
standing pod, and the pass after that creates the claim and the pod.
The first start on the new claim is one full sync, on the numbers
above. Every later start is a delta. No `Catalog` needs a change,
because the default class binds.

Nothing else moves. The catalog pod, the worker `Job`s, the
`CronJob`s, the `Library` catalog claims, the catalog `Service`, and
the `EndpointSlice` are untouched. No database is rebuilt, because a
screen's copy is a replica and the durable catalog is the catalog pod's.

## Proof

On `liken-1`, on one `Player` with a display.

Before the change, on the release that runs now: restart the screen pod
three times and record, each time, the wall time from the pod's start
to the browser's first draw of the wall, and the sidecar's resident
memory when the sync completes. Record the catalog's row count beside
them, so the times are comparable with the numbers after.

After the change, on the cluster's default class, `local-path`:

- The first start on the fresh claim: the wall time to the first draw
  and the sidecar's memory at the end of the sync, against the 512Mi
  limit.
- Three more restarts of the same pod: the wall time to the first draw
  each time, and the sidecar's memory each time. This is the number the
  plan is for.
- The claim's `volume.kubernetes.io/selected-node` annotation and the
  node the pod ran on, which must be the same node three times.
- The bytes used on the claim against `spec.storage.size`.
- `corrosion cluster members` from inside the catalog pod, before the
  three restarts and after, with the actor ids. One screen is one actor
  across all three.

Then the move: point the `Player` at a display on another machine, or
move the machine's display claim, so the screen pod must land
elsewhere. Record the time from the pod becoming unschedulable to the
wall drawing again, that exactly one claim was deleted, and that the
new claim bound on the new node.

### The drill, 2026-09-02

Built in release 2026.09.02-009 and drilled on `liken-1` the same
day, on the `lab-portable` `Player`, with 53,614 rows in the
namespace's catalog. The drill measured the time to the full row
count on the screen's agent against the catalog pod's, rather than
the first draw, because nothing reads the screen from `vega`.

| Screen restart | Before, on `emptyDir` | After, on the claim |
|---|---|---|
| Time to the full catalog | 157 s | 0 to 1 s, three times |
| Sidecar memory after | 211 MiB | 11 MiB |
| First start on a fresh claim | every start | once: 152 s, 209 MiB |

The claim bound on `local-path` on `stick-1`, and the pod came back
to that node on every restart. `Catalog.status.screens` listed the
screen with its claim, node, and phase. The move drill did not run,
because the testbed has one display, and the bytes used on the claim
and the actor ids were not read. All three wait for the next drill.

## What is set aside

Reusing `Catalog.spec.storage.storageClassName` for the screens. It
is no CRD growth, and a cluster whose catalog pod already binds that
class would get screens that bind too. It loses because it gives a
person no way to keep the namespace's one durable copy on durable
storage and the screens on the machines they draw on.

An opt-in block, with `emptyDir` as the default. It loses because a
screen with no claim pays the full sync on every start, which is the
problem, and because every `liken` cluster ships a default class that
binds.

A size of the screens' own. A screen holds the same rows the durable
catalog holds, so one size is the true one, and a second field would
only let the two disagree.

A field on `Player`. `media-operator` owns that resource, and this
layer states nothing in it.

The node in the claim's name, one claim per node. The operator cannot
name the claim until the scheduler has placed a pod, and the pod cannot
be placed until it names a claim.

Reading the display `ResourceClaim`'s allocation to learn the node
before the pod is scheduled. It needs a `resourceclaims` grant this
operator does not hold, and it ties this operator to the shape of
`media-operator`'s allocation results.

A `StatefulSet` per screen, which would give each screen a claim
`volumeClaimTemplates` provisions. Plan 06 set aside a `Deployment` per
screen for the same reason: the pod's recreate is already this
operator's pass, and a workload controller adds an API group and a verb
for a job the operator does without one.

One volume shared by every screen, `ReadWriteMany` or `ReadOnlyMany`.
Two agents cannot write one SQLite database, and a catalog read from a
shared volume is the Litestream design in
[`rejected/`](../rejected/litestream-as-the-catalog-transport.md).

Keeping plan 06's restart of the sidecar. It was never built, the peak
fits under the limit, and with a claim it is paid once per claim.

## What is not decided

The unschedulable grace before the recovery delete. Five minutes is the
starting number, and the drill's move step sets it. Too short throws
away a good cache during a node's brief absence, and too long leaves a
moved screen dark.

Whether the catalog pod's own ingest peak needs anything. That is plan
28's open question and this plan does not answer it.

How long Corrosion carries an actor it has declared down, and whether
the dead actors an `emptyDir` screen has already produced clear on
their own. The drill records the member list, and the answer may be a
note rather than code.

Whether a full claim needs a check of its own, on the screen or
anywhere else. No agent in this design reports its volume usage today.

Whether the recovery should also fire for a claim bound on a node the
cluster no longer has.
