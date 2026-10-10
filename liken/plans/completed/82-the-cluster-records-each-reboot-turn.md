# 82. The cluster records each reboot turn before it grants it

Milestone 82. Proposed and built 2026-10-10. The drill on the
dev-cluster has not run yet ([The drills](#the-drills)). It closed the
last part of the open problem "Coordinate cluster-operator instances",
the part that its section "What stays open" described: a paused former
leader of `cluster-operator` can land a reboot grant after a new leader
made its own decision, and the two grants together exceed the
disruption budget. The design is option 1 of that document, the grant
ledger. It depends on the election of
[root plan 80](../../../plans/completed/80-one-leader-election.md) and on
the rollback floor of `2026.09.28-001`. It changes `cluster-operator`
and the `Cluster` CRD. It does not change the machine operator.

## The problem

`decideRollout` in `cluster-operator/rollout.go` decides which
machines may reboot now, from one view of the fleet.
`carryOutRollout` then writes each grant, a `RebootApproved`
condition, onto each `Machine`'s status. Each write is conditional on
the `resourceVersion` of that one `Machine`. Nothing ties the grants
of one leader to the decision of the next.

The `Lease` election does not fence a former leader. The write guard
refuses a write once the last renewal is 10 seconds old, and the
client abandons a request after 15 seconds. A write that passed the
guard is already past that check. So a leader that stops after the
check and before the send, for example on a stalled node or under
`SIGSTOP`, sends the write when it resumes. A slow API server can
also commit a write that the client already abandoned.

The race takes these steps, with a budget of one:

1. Copy A leads. `node-2` waits for a turn, and `node-1` does not
   ask yet. A decides to grant `node-2`, and its grant write passes
   the write guard.
2. A's process stops for 30 seconds or more before the request
   leaves.
3. `node-1` stages a change and asks for a turn.
4. Copy B takes the `Lease`. It reads the fleet: no machine holds a
   grant, and `node-1` and `node-2` both wait. Name order puts
   `node-1` first, so B grants `node-1`. `node-1` drains and
   reboots.
5. A resumes and sends its grant to `node-2`. The write names the
   `resourceVersion` that A read in step 1. Nothing wrote `node-2`
   since, so the API server accepts it, and `node-2` drains and
   reboots too.

Two machines are down under a budget of one. When both are leaders,
the leader floor fails too, and a fleet of three leaders loses its
etcd quorum. The per-object condition does not help, because the
`resourceVersion` of `node-2` never changed between A's read and A's
write.

## The design

The `Cluster`'s status holds the set of machines that hold a reboot
turn now. This is the grant ledger. A machine enters the ledger
before its grant is written, and leaves it only after the grant is
gone and no older grant write can land. Every change to the ledger
is a write of the `Cluster`'s status, conditional on the `Cluster`'s
`resourceVersion`. Two copies that read the same `Cluster` cannot
both add an entry, because the second write gets `409 Conflict`. So
the budget becomes one compare-and-swap on one object.

The Kubernetes eviction API uses the same pattern. Before it deletes
a pod, the API server records the pod in the `PodDisruptionBudget`'s
`status.disruptedPods` with a conditional write, and the disruption
controller removes the record when it sees the pod gone. `kured`
holds its reboot lock in an annotation on its own `DaemonSet` for the
same reason.

### The field

`status.rebootTurns` is a list. Each entry names one machine that
holds a reboot turn or is about to receive one:

```yaml
status:
  rebootTurns:
    - machine: node-4
      machineResourceVersion: "184467"
      since: "2026-10-10T18:02:11Z"
```

* `machine` is the `Machine`'s name. The list is a map keyed by
  `machine` (`x-kubernetes-list-type: map`), so a machine appears
  once.
* `machineResourceVersion` is the `resourceVersion` of the `Machine`
  that the grant write names. The operator compares it only for
  equality, as the `operators` skill requires. The section "Leave the
  ledger" gives the reason for the field.
* `since` is when the entry entered the ledger. It tells a person how
  long a turn has been open when the `Machine` holds no grant.

In Go, `cluster.ClusterStatus` gains `RebootTurns []RebootTurn`, and
`RebootTurn` holds `Machine`, `MachineResourceVersion`, and `Since`.

`kubectl get clusters -o wide` shows the field in a new printer
column, `Turns`, with the JSON path `.status.rebootTurns[*].machine`
and `priority: 1`:

```
NAME   STATUS     MACHINES   VERSION          NEWEST           AVAILABLE        LEADERS                    ENDPOINT   AGE   TURNS
lab    Updating   4/5        2026.10.12-001   2026.10.12-001   2026.10.12-001   ["node-1","node-2","node-3"]   ...    9d    node-4
```

The `Progressing` condition keeps its message, which names each
machine that takes a turn and each machine that waits. The `Cluster`
reference page in the manual is generated from the CRD, so the
field's schema description is its manual entry.

### Who counts against the budget

`decideRollout` counts a slot in flight for each machine that is in
the ledger, holds a grant, or is unavailable. A machine that matches
more than one of these takes one slot. A leader in the ledger sets
the leader floor, the same as a leader that holds a grant. A machine
in the ledger is never a new grant, because its turn is already
decided.

### Enter the ledger

A sweep that grants turns adds one entry for each machine it grants,
with the `resourceVersion` of the `Machine` copy it decided from. It
writes the `Cluster`'s status first. Only after that write succeeds,
and its answer holds every new entry, does the sweep write each
grant. The grant write sends the same `Machine` copy, so it names
exactly the `resourceVersion` that the entry records.

The sweep writes the `Cluster`'s status once, before any `Machine`
write. Today `publishClusterStatus` writes at the end of the sweep,
after `carryOutRollout` and `markLost`. This milestone moves it ahead
of them and puts the ledger into the same write, so a sweep that
grants costs no more `Cluster` writes than it does now. The
`Progressing` message then names a turn a moment before its grant
lands, which is the order the ledger needs.

### Leave the ledger

An entry leaves the ledger only when a read from the API server shows
one of two things:

* The `Machine` does not exist (`404`).
* The `Machine` holds no grant, and its `resourceVersion` differs
  from `machineResourceVersion`.

A grant write names `machineResourceVersion`. The API server accepts
it only while the `Machine` is still at that version. A
`resourceVersion` never returns to an earlier value, so once the
`Machine` is at another version, no grant write that this entry
covers can land. A write of the `Machine` by anyone moves the
version: the grant itself, a reclaim, a `Lost` verdict, or the
machine operator's own status write.

The read goes to the API server, not to the watch's copy, because a
copy can be older than the version that the entry records. A copy at
an older version differs from the entry, and would free the slot
while the grant can still land. The `operators` skill allows a direct
read where "the read must see another writer's write at once", and
this is such a read. The sweep sends it only for an entry whose
`Machine` copy shows no grant. A settled fleet has an empty ledger,
and a turn in progress holds a grant, so the read happens about once
for each finished turn.

The normal life of a turn is:

1. Sweep N adds `node-4` at version 184467, writes the `Cluster`, and
   then grants `node-4`. The grant moves the `Machine` to 184470.
2. `node-4` drains, reboots, comes back, and asks for nothing. Sweep
   M reclaims the grant (`revoke` in `decideRollout`). The `Machine`
   moves to 184702.
3. The reclaim's own watch event wakes sweep M+1. The `Machine` copy
   shows no grant, so the sweep reads `node-4` from the API server,
   finds 184702 and no grant, and removes the entry in its `Cluster`
   write. The same write can add the next machine's entry, so the
   freed slot goes to the next machine in one write.

### Confirm an entry

The entry from a leader that stopped, or from a write whose answer
was lost, can name a `Machine` that holds no grant at the recorded
version. A grant write for it can still be in flight. The sweep then
confirms the entry: it writes the grant itself, from the copy it just
read from the API server, so the write names the recorded version.
The two writes name the same version, so the API server accepts at
most one of them. After either lands, the `Machine` holds a grant at
a new version, and the turn continues as normal. When the confirm
gets a `409`, the version has moved, and the next sweep's read
settles the entry.

A write that changes nothing does not move the `resourceVersion`.
The API server compares the new object with the stored one, and when
they are equal it stores nothing and answers the old version. So a
reclaim of a grant that is absent fences nothing. The confirm must be
a write that changes the `Machine`, and the grant is that write.

The sweep confirms an entry only when the `Machine` reads available
and the heartbeat record is old enough (`listening` is false). A
grant on a machine that is down would make `effectivePhase` read it
`Updating` for `rolloutStallAfter`, 10 minutes, and hide that it is
`Lost`. A machine that is down needs no confirm, because `markLost`
writes its status and moves the version.

When the machine reads available but no longer asks for a turn, the
confirm still writes the grant, and the next sweep reclaims it. The
slot is already in the ledger, so the grant cannot exceed the budget
in the meantime. Decision 4 keeps this.

### Adopt a grant

A `Machine` can hold a grant that no entry names. An older
`cluster-operator` wrote it, or the ledger field was pruned (see "The
served schema"). The sweep adds an entry for each such machine, with
the `Machine`'s current `resourceVersion`, in its `Cluster` write.
Adoption does not check the budget, because the grant exists already.
It only takes slots, so a ledger over the budget grants nothing new
until it drains. Adoption also runs while `listening` is true,
because it only takes slots.

### A new leader inherits the ledger

A new leader needs no step of its own. The rules above read only the
API's state, so a new leader applies them to the entries that an
earlier leader wrote, the same way the earlier leader would. For each
inherited entry:

* The `Machine` holds a grant: the slot is in use, and the turn runs
  to its reclaim.
* The `Machine` holds no grant, and the API server answers it at the
  recorded version: a grant write can still land. The new leader
  confirms the grant, or waits for `markLost` when the machine is
  down. The slot stays in the ledger.
* The `Machine` holds no grant at another version, or does not exist:
  no grant of this entry can land. The new leader removes the entry,
  and the slot is free.

In the race above, the ledger holds `node-2` from step 1. B counts
`node-2`'s slot, so B grants `node-1` nothing. B reads `node-2` from
the API server at the version that A recorded, and confirms the
grant. A's late write and B's confirm name the same version, so one
of them lands and the other gets a `409`. One machine reboots.

If A stopped before its `Cluster` write, A's write names a `Cluster`
version that B has since moved, and gets a `409`. A sends no grant,
because it writes a grant only after the `Cluster` write succeeds.

The same rules cover a single leader whose request timed out. The
operator's memo notes that it holds no current copy after a failed
write, and the next sweep reads the `Cluster` and the `Machine` from
the API server and finds one of the three cases above.

### The order of the writes

| Write | Object | When | Order |
|---|---|---|---|
| Add an entry | `Cluster` status | The sweep grants a turn | Before the grant |
| Adopt an entry | `Cluster` status | A `Machine` holds a grant that no entry names | Before any new grant in the same write |
| Grant | `Machine` status | The `Cluster` write succeeded and its answer holds the entry | After the `Cluster` write |
| Confirm | `Machine` status | An entry's `Machine` reads available, with no grant, at the recorded version | After the `Cluster` write |
| Reclaim | `Machine` status | The machine is available and asks for nothing | Any time; it never adds disruption |
| Remove an entry | `Cluster` status | A read from the API server shows the `Machine` gone, or with no grant at another version | Before the slot is used, in the write that uses it |

### Conflicts

* **The `Cluster` write gets a `409`.** Another copy wrote the
  status. The sweep writes no grant and no confirm. It still writes
  its reclaims and its `Lost` verdicts, because each one is
  conditional on its own `Machine` and none adds disruption. The memo
  notes no current copy of the `Cluster`, and the next sweep reads it
  from the API server and decides again.
* **A grant gets a `409`.** The `Machine` changed after the decision,
  for example a status write by its own operator. The entry stays.
  The next sweep reads the `Machine` at another version with no
  grant, removes the entry, and, if the machine still asks and the
  budget allows, adds it again at the new version in the same write.
* **A confirm gets a `409`.** The version moved. The next sweep's
  read shows either a grant (the turn runs) or another version (the
  entry leaves).
* **A reclaim gets a `409`.** The next sweep tries again, as it does
  now.

### Crash points

The sweep keeps nothing in memory between passes that the rules need,
so a crash at any point leaves a state that the next leader settles
from the API alone.

| The process ends | State left | The next sweep |
|---|---|---|
| Before the `Cluster` write | Nothing changed | Decides from scratch |
| With the `Cluster` write in flight | The entry may or may not exist | If it exists: confirm. If not: decide from scratch |
| After the `Cluster` write, before the grant | Entry, no grant, recorded version | Confirm |
| With the grant in flight | The grant may land late | Confirm; the API server accepts one of the two writes |
| After the reclaim, before the entry leaves | Entry, no grant, another version | Remove the entry |
| With the removal in flight | The entry may or may not exist | Remove it again, or find it gone |

### The served schema

The API server prunes a status field that the served CRD does not
declare, and answers the write without it, with no error. That
happens while a newer `cluster-operator` runs against the older CRD:
in the seconds after the first leader boots a new release, and when a
leader on an older release starts k3s again and its auto-deploy
applies its own copy of the CRD. The init's schema-revision guard
(`init/schemarevision.go`) protects only the copy on the same
machine's disk.

So a grant needs proof that the ledger is stored. The answer of the
`Cluster` write must hold every entry that the write added. When it
does not, the sweep writes no grant and no confirm, and the
`Progressing` message says that reboot turns wait for a `Cluster`
schema that stores `status.rebootTurns`. `putStatus` in
`liken/kubernetes` decodes only the `resourceVersion` today, so
`PublishClusterStatus` must answer the stored ledger too.

While the older schema is served, every read shows an empty ledger.
The rules then adopt each grant again when the newer schema returns,
and grant nothing while it is absent.

### Upgrade and rollback

The `cluster-operator` pod runs the image `liken.sh/cluster-operator:installed`,
which resolves on each node to the build that node's OS carries.
During a rollout, the acting copy can therefore run an older binary
or a newer one, and it can change from one to the other each time a
node drains.

* **An older binary leads.** It does not know the ledger. Its
  `Cluster` status write sends the whole status from its own struct,
  which has no `rebootTurns`, so the write removes the ledger. Its
  grants enter no ledger. It is unfenced, exactly as every release is
  today.
* **A newer binary leads after an older one.** It finds grants that
  no entry names and adopts them before it grants a new turn.
* **A paused older binary and a newer leader.** The older binary's
  late grant is not fenced, because it wrote no entry. This window
  closes when no node carries a release from before the ledger.

A rollback to a release from `2026.09.28-001` up to the last release
without the ledger is allowed, and the floor does not change. The
older binary runs as it ran before, and the ledger is empty while it
leads. The newer CRD stays where the schema-revision guard keeps it,
and an older binary ignores the field. A roll forward adopts the
grants that the older binary wrote. The proposal asked whether to
raise the floor instead, and decision 6 keeps it.

### The machine operator

The machine operator needs no change. It reads the grant
(`machine-operator/reconcile.go`), carries it through its own status
writes, and on a `409` takes the grant from a fresh read
(`publishOwnStatus` in `ownstatus.go`). The ledger is invisible to it.
A machine operator of any release works with a ledger-aware
`cluster-operator`.

### The API change

`status.rebootTurns` is a new optional status field in `v1alpha1`.
The CRD serves one version, and the field is additive, so it needs no
new API version and no conversion webhook. An object stored without
the field reads as an empty ledger.

The CRD's `liken.sh/schema-revision` rises from `2` to `3`. Revision
`2` is the rollback floor's rule on `spec.version`. The pin test in
`init/schemarevision_test.go` holds the revision and the schema
together.

The schema:

* `rebootTurns`: an array, `x-kubernetes-list-type: map`,
  `x-kubernetes-list-map-keys: [machine]`, `maxItems: 1024`.
* `machine`: a required string, `maxLength: 253`, the pattern of a
  DNS subdomain, the same as a `Machine`'s name.
* `machineResourceVersion`: a required string, `maxLength: 64`.
* `since`: a required string, `format: date-time`.

No CEL rule compares the ledger with `spec.disruption`. A rule on the
whole object runs on spec writes too, so a person could not lower
`maxUnavailable` while more turns are open than the new budget
allows.

The RBAC does not change. `cluster-operator` already holds `get` on
`machines`, and `update` on `machines/status` and `clusters/status`.

## Alternatives considered

1. **A fencing token in each grant.** The grant names the `Lease`'s
   holder and its `leaseTransitions`, and the machine operator reads
   the current `Lease` before it drains, and ignores a grant from an
   earlier holder. This changes the shape of the grant and both
   operators. Every machine operator needs `get` on the
   `liken-cluster-operator` `Lease`. A machine operator from an older
   release ignores the token, so the fence holds only after every
   machine runs the new release, and a rollback removes it. The
   ledger changes one component and holds whatever machine operator
   runs.
2. **Narrow the window.** One grant for each sweep, with a direct
   read of every `Machine` just before it. This needs no API change.
   It makes the window shorter and does not close it: a paused leader
   still sends its write after the new leader decides.
3. **Accept the risk.** The race needs a pause of about 20 seconds or
   more between the write guard and the send, or a write that the API
   server commits long after the client gave up. Both are rare. The
   cost when they happen is two machines down, and with two leaders
   down, a fleet of three leaders loses its etcd quorum. The fix
   stays in one component and one status field, so the cost of the
   fix is lower than the cost of the failure.

## Tests

Each test runs in a `synctest` bubble against a fake API server
served through `kubernetes/apiservertest`, and waits on the fake
clock. No test sleeps in real time.

`liken/kubernetes/fakeapi` checks no `resourceVersion` on an update
today. The tests below need it to refuse an update from a stale
version with `409`, and to keep the version of an update that changes
nothing, as the API server does. The fake gains both behaviors as an
option that a test turns on, with a test of its own in
`fakeapi_test.go`, so the existing sweep tests do not change.

1. **The decision.** Table tests of `decideRollout`, flat, one case
   per row:
   * an entry with no grant takes a slot and a leader entry sets the
     floor;
   * an entry whose `Machine` reads available with no grant at the
     recorded version is confirmed;
   * the same entry on a machine that is down is not confirmed;
   * an entry at another version with no grant leaves, and its slot
     goes to the next waiting machine in the same decision;
   * a grant that no entry names is adopted, over the budget if it
     must be, and blocks new grants;
   * `listening` holds grants and confirms, and not adoption or
     removal.
2. **The order.** A sweep test reads the fake's request log and
   checks that the `Cluster` status write comes before each grant
   write. A `Cluster` write that gets a `409` sends no grant. A
   `Cluster` answer without `rebootTurns`, the answer of an older
   schema, sends no grant, and the `Progressing` message says why.
3. **The paused leader.** This test replays the race. Two
   `fleetReader`s, A and B, each with its own client, read one fake
   API server that checks versions. A gate in front of the fake holds
   one request until the test releases it. After each write, the fake
   counts the machines that hold a grant or are unavailable, and the
   test fails if the count ever exceeds the budget, or if two leaders
   are ever counted. One table row for each pause:
   * A stops after its `Cluster` write, before its grant;
   * A's grant is held in the fake, and lands after B's sweeps;
   * A's `Cluster` write is held, and lands after B's write;
   * A's grant is abandoned by A's client timeout, and the fake
     applies it after the answer is lost, as `electiontest` does
     with `landAfterRead`.

   In each row, B sweeps while A waits, then the gate releases A,
   then B sweeps again. The machine that A chose ends with one grant,
   the other machine still waits, and the budget holds at every
   write. Written first, the test fails on today's `rollout.go` in
   the first, second, and fourth rows, which proves that it replays
   the race. The third row has no `Cluster` write to hold before
   this milestone.
4. **The election and the ledger together.** Two copies run `run`
   with real elections against `kubernetes/election/electiontest`
   and the fake. The test stops A's transport after its `Cluster`
   write for 40 seconds of the fake clock. B takes the `Lease` and
   confirms A's entry. A's transport resumes, and A's late grant gets
   a `409`. This test shows that the rules hold under the real
   timings, and test 3 carries the proof.
5. **The crash points.** One row for each row of the crash table. A
   sweep stops after the named request, a new `fleetReader` with an
   empty memo sweeps, and the test checks the state the table names.
6. **Upgrade and rollback.** A fleet with grants and no ledger: the
   first sweep adopts every grant before it grants a new turn. A
   status write that drops `rebootTurns`, as an older binary sends:
   the next sweep adopts the grants again.
7. **The schema.** The pin test fails until the revision is `3`. A
   test reads the CRD and checks the list map keys, the field types,
   and the printer column. The docs' generate test renders the new
   field.

## The drills

The OS image carries `cluster-operator`, with `imagePullPolicy:
Never`, and the OS publishes no development builds. So liken-1 runs
the new binary only after a release of `liken`.

### On the lab fleet, before the release

The `lab` fleet takes a lab release with `make release
VERSION=<date>-9xx` (the `dev-cluster-drills` skill). Its budget is
one, and `node-1` to `node-3` are leaders.

1. **The API server's behavior.** On a scratch CRD in
   `drills.example.com`, as the schema drills reference describes:
   a status `PUT` that changes nothing keeps its `resourceVersion`, a
   `PUT` from an older version gets `409`, and a status field that
   the schema does not declare is absent from the answer.
2. **Upgrade into the ledger.** Upgrade the lab from the newest
   release into the lab release. Record the first adoption, every
   `Cluster` write before each grant, and `kubectl get clusters -o
   wide` during the rollout.
3. **A stale entry.** With the fleet settled, set `cluster-operator`
   to two replicas. Write an entry for `node-5` at its current
   version into the `Cluster`'s status by hand, with `kubectl edit
   --subresource=status`. The leader confirms the grant on `node-5`,
   and no other machine takes a turn until `node-5`'s turn ends.
4. **A stale grant.** Save `node-5`'s `Machine` before the confirm in
   step 3. After it, send the saved copy with a grant through
   `kubectl replace --subresource=status`. The API server answers
   `409`.
5. **A paused leader.** During a rollout, stop the leader with
   `SIGSTOP` from a `kubectl debug node/<node> --profile=sysadmin`
   pod while a machine holds a turn, wait for the other copy to take
   the `Lease`, and send `SIGCONT`. The ledger never holds more than
   one entry, and the resumed process exits.
6. **Rollback.** Point `spec.version` at the release before the lab
   release. The older binary leads and the ledger empties. Point it
   back. The newer binary adopts each open grant.

Step 5 cannot place the pause between the `Cluster` write and the
grant on purpose, because the two writes are microseconds apart.
Steps 3 and 4 build that state by hand, and test 3 replays it
exactly.

### On liken-1, after the release

liken-1 has two machines, `liken-1` and `stick-1`, with a budget of
one. After the release that carries the ledger, repeat steps 2 to 5.
Before the release, only step 1 can run there, on a scratch CRD; the
`cluster-operator` of liken-1 stays the one its OS carries.

## The manual and the comments

* `cluster/manifests/clusters-crd.yaml`: the field's description, the
  `Turns` column, and the descriptions of `spec.disruption`, the
  `Progressing` message, and `status`, which say that the cluster
  records each turn before it grants it. The `Cluster` reference page
  regenerates from them.
* `docs/content/docs/guides/upgrade.md`, "The rollout": the cluster
  records each turn in `status.rebootTurns` before it grants it, and
  `kubectl get clusters -o wide` shows the open turns.
* `docs/content/docs/guides/troubleshoot.md`: an entry in
  `status.rebootTurns` for a machine with no grant, what
  `cluster-operator` does with it, and the `Progressing` message when
  the served schema does not store the field.
* `docs/content/docs/guides/rollback.md`, "The oldest release you can
  roll back to": a release before the ledger leads without it, and a
  roll forward adopts its grants.
* Then the `skills` target, which regenerates `liken:upgrade`,
  `liken:troubleshoot`, and `liken:rollback`.
* `cluster-operator/rollout.go`, `fleet.go`, and `leader.go`: the
  head comments describe the ledger and the order of the writes.
  `leader.go` says that the ledger, not the election, keeps the
  budget against a paused leader.
* The open problem: its sections "What stays open" and "Options"
  leave, and "Verification needed" keeps only the parts that this
  plan's drills do not run.

## Decisions

The open questions of the proposal were answered on 2026-10-10, before
the build:

1. **The names.** `status.rebootTurns`, with the fields `machine`,
   `machineResourceVersion`, and `since`.
2. **The column.** `Turns` shows only in `kubectl get clusters -o
   wide`, at `priority: 1`.
3. **`Event`s.** No new reason. Adding and removing an entry post
   nothing, because they go with `RebootTurnGranted` and with the
   reclaim that ends a turn. A confirm posts `RebootTurnGranted` only
   when its write lands, with the message "the cluster recorded this
   machine's reboot turn in an earlier sweep, and grants it now".
4. **The confirm of a machine that no longer asks.** The confirm
   writes the grant and the next sweep reclaims it, two writes, so a
   slot never stays held by an entry that nothing moves.
5. **One `Cluster` write.** The sweep writes the `Cluster`'s status
   once, ahead of every `Machine` write, with the ledger in the same
   write.
6. **The rollback floor.** It stays at `2026.09.28-001`. Commit
   3023a787 added the CEL rule and took schema revision 2, so the
   ledger takes revision 3.
7. **`maxItems: 1024`.** Kept.
8. **A confirm while the template-lag read fails.** Confirms run.
   `withoutGrants` holds only the new turns, and takes their entries
   out of the ledger.

## What the build changed from the proposal

* **A reclaim frees its slot one sweep later.** Before the ledger, a
  sweep that reclaimed a spent grant could give the slot to the next
  machine in the same decision. The entry now holds the slot until the
  next sweep reads the `Machine` without the grant, as "Leave the
  ledger" requires. The reclaim's own watch event wakes that sweep, so
  the delay is one sweep. `TestRolloutRevokesAfterTheMachineConverges`
  changed to say so.
* **A pruned ledger costs a second write.** The sweep learns that the
  served schema dropped the field only from the answer of its `Cluster`
  write. It then writes the status once more, with the `Progressing`
  message that names the schema (`schemaPrunedTurns` in `fleet.go`).
  While an older CRD is served, each sweep that would grant a turn
  makes two `Cluster` writes.
* **Removal goes on while `listening`.** Test 1's row says that
  `listening` holds grants and confirms, and not adoption or removal.
  The build follows that row: an entry leaves on the API server's
  answer alone, because that answer does not depend on the heartbeats.
* **The election test pauses for two minutes, not 40 seconds.** Copy B
  takes the `Lease` 30 to 41 seconds after A's last renewal, and then
  judges the heartbeats for `HeartbeatStaleAfter`, 40 seconds, before
  it confirms anything. A pause of 40 seconds lets A's late grant land
  first, which the budget also survives, but does not reach the
  confirm.

## What the tests showed

Test 3, written first, against the `rollout.go` of commit 23c9bfd5,
with a budget of one:

```
--- FAIL: A stops after its Cluster write, before its grant
    the budget of one was exceeded: ["PUT /.../machines/node-2/status left out: node-1, node-2" ...]
    machines with a grant = ["node-1" "node-2"], want ["node-2"]
--- FAIL: A's grant is held in the API server and lands after B's sweeps
    the budget of one was exceeded: ["PUT /.../machines/node-2/status left out: node-1, node-2" ...]
    machines with a grant = ["node-1" "node-2"], want ["node-2"]
--- FAIL: A's Cluster write is held and lands after B's write
    machines with a grant = ["node-2"], want ["node-1"]
--- FAIL: A's client abandons its grant and the API server applies it later
    the budget of one was exceeded: ["PUT /.../machines/node-2/status left out: node-1, node-2" ...]
    machines with a grant = ["node-1" "node-2"], want ["node-2"]
```

The third row, A's `Cluster` write held, kept the budget before the
ledger, because A's grant went out before its `Cluster` write. It
failed only on the machine that the ledger gives the turn to. With the ledger, all four rows pass, and the budget holds at
every write. Test 4 ran two copies with real elections: B took the
`Lease` 89 seconds into the bubble's clock, confirmed `node-2` 70
seconds later, and A's late grant got `409`.
