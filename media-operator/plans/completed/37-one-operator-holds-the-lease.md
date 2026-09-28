# 37, One operator holds the Lease

Built on 2026-09-27. The operator runs client-go's leader election
(`k8s.io/client-go/tools/leaderelection`) with a `LeaseLock` on the
`coordination.k8s.io/v1` `Lease` named `media-operator` in its own
namespace. Only the copy that holds the `Lease` opens a bus session and
reconciles. A second copy, from a rollout, a node partition, or a
replica count above one, waits and changes nothing. On `SIGTERM` the
leader finishes its pass, ends its bus session, and releases the
`Lease`, and a waiting copy takes it on its next read, within about 11
seconds. A leader that cannot renew exits before a waiting copy can
take its `Lease`. The `Deployment` rolls with
`RollingUpdate`, so a rollout starts the new pod as a waiting copy
while the old one still leads. This plan closes the open problem "two
operators can run at once". No drill has run on `liken-1` yet.

## The problem

The operator must run as one instance. It creates pods, deletes them,
and writes every `Play`'s status, and two instances that do that at the
same time race: both create a pod for one `Play`, or one deletes what
the other just made, or two status writes overwrite each other. The
`Deployment` sets `replicas: 1`, which asks for one instance but does
not enforce it.

Three cases run a second instance, and `replicas: 1` prevents none of
them. A rolling update starts the new pod before it stops the old one,
so a deploy runs two operators for a few seconds. A node partition
makes the `Deployment` controller start a replacement while the old pod
still runs on the unreachable node, so two operators run until the
partition heals. And a person edits `replicas` to `2`, by hand or by a
bad automation, and two operators run by design.

The last case is the reason the fix belongs in the operator code. A
person can patch a guard in the `Deployment` spec away. A guard in the
code applies whatever the spec says.

This operator differs from the hardware operators. The
`audio-operator`, `display-operator`, and `bluetooth-operator` are
`DaemonSet`s: one instance per machine is correct, because each one
manages the hardware on its own machine. This operator is a cluster
singleton, one instance for the whole cluster, and a `Deployment` has
no built-in limit of one instance.

`strategy: Recreate` on the `Deployment` fixes only the rollout case.
It stops the old pod before it starts the new one, so a rollout no
longer overlaps. A partition and a `replicas` patch still run two
operators, because `Recreate` controls when pods are replaced and
leaves the replica count to the spec.

## The design

**client-go's election.** `leader.go` builds a `LeaderElector` with a
`resourcelock.LeaseLock` over the typed coordination client, from the
in-cluster configuration. The operator's other API access is its own
small client, and the organization chose client-go's election for every
operator that is not a `DaemonSet`, so the election uses the package that already holds the algorithm and
its tests. The identity is the pod's name and a random suffix, for
example `media-operator-7d9f8b6c5d-x2k4p_1a2b3c4d`, so a restarted
container is a new candidate and waits for the `Lease` its earlier
process held.

**Before anything acts.** `operate` calls `lead` after the metrics
listener starts and before the bus session opens. A copy that waits
for the `Lease` serves `/metrics` and does nothing else. The bus
matters here too: two copies with one MQTT client id would end each
other's sessions.

**The timings.** The `Lease` lasts 30 seconds, twice client-go's
default. The renewal deadline is client-go's 10 seconds. The retry
period is 5 seconds, slower than client-go's 2 seconds. The leader
renews once per retry period, twelve writes a minute. A waiting copy
reads the `Lease` every 5 to 11 seconds, because client-go adds up to
1.2 retry periods of jitter, so about five to twelve reads a minute. A
cluster can already have several clients that renew a `Lease` every one
or two seconds, and the operator does not need faster failover. A
waiting copy takes a released `Lease` on its next read, within about 11
seconds, and an abandoned one 30 to 41 seconds after the last renewal.
A steady leader renews with an update from the version it wrote last,
and does not read the `Lease` before each update.

The duration is the safety margin. After its last renewal at T, the
leader tries again at T+5s and gives up at T+15s, when the renewal
deadline passes. client-go then tries to release the `Lease`, bounded by
one more renewal deadline, and only then calls `OnStoppedLeading`, so
the leader exits by T+25s. A waiting copy takes the `Lease` 30 seconds
after it saw the last renewal, after T+30s. With client-go's 15-second
duration the two times would meet.

**A loss ends the process.** client-go calls `OnStoppedLeading` when
the renewals fail for the renewal deadline, or when another process
holds the `Lease`. The operator then exits with code 1, because a pass
in flight must not keep writing after another copy can take the
`Lease`. The kubelet restarts the container, and it waits as a new
candidate with empty memory. The timings above make a running leader
exit before another copy can take the `Lease`.

**The election is not fencing.** client-go documents this limit. A
leader that pauses, for example on a stalled node, can resume after its
`Lease` expired and finish a request it had already sent, while another
copy leads. The API server's resourceVersion checks still refuse a
stale status write, and a duplicate create of a pod or a claim fails
with a conflict, but a delete in flight can still land.

**A shutdown releases the `Lease`.** `ReleaseOnCancel` is on. On
`SIGTERM` the pass loop finishes its pass, and `stepDown` stops the bus
session and waits for its reader to return, because a press or a
report on the bus can write to the API. Then it ends the election, and
client-go writes the `Lease` with no holder. A waiting copy takes it on
its next read, not after the `Lease` expires.

client-go's release can fail on a normal shutdown. The cancel ends its
renewal loop, but a renewal already sent can reach the API server after
the release read the `Lease`, so the release's update carries a stale
resourceVersion and the API server refuses it. So after the election
ends, `end` reads the `Lease` again and clears it itself if it still
names this process. The late renewal can land after that read too, so
a conflict reads the `Lease` again, up to three times within 5 seconds.
The renewal loop sends one renewal at a time, so at most one late write
is in flight. If the bus session does
not stop within 5 seconds, `stepDown` does not release the `Lease`: the
process exits holding it, and a waiting copy takes it when it expires,
after any write the session still makes can land.

**The rollout.** The `Deployment` uses `RollingUpdate` with `maxSurge:
1` and `maxUnavailable: 0`. The new pod starts, waits for the `Lease`,
and the old pod stops once the new one runs, so the image pull happens
before the old pod stops. The reconcile then stops for the old pod's
last pass and bus stop, the release, up to 11 seconds until the new pod
reads the `Lease`, and the new pod's first lists. `replicas` stays 1, and the manifest says that 2 is safe.

**Permissions.** A `Role` in the operator's namespace grants `get` and
`update` on the one `Lease` by name, and `create` on leases, because
RBAC cannot read a name off a create.

**The cost, and the pod build.** client-go's `resourcelock` package
imports the whole typed clientset, `k8s.io/client-go/kubernetes`, and
its scheme. The organization accepts that cost for the operator, one
pod per cluster, and not for the roles that run in every playback pod
and every `Remote`'s pod. So the program has two builds:

* The full build, `/media-operator`, which the operator runs.
* The pod build, `/media-operator-pod`, built with the tag `pod`.
  `leader_pod.go` replaces `leader.go` in it, and refuses to run the
  operator, so client-go stays out. The command sidecar and the reader
  run it from the sidecar image. The player image holds it alone for
  the player shim, and the api image holds it alone for the api.

The operator image holds both builds, and the sidecar image is still
the operator image under a second name, so a node that runs a sidecar
pulls both builds, about 40MB where the one build before this plan was
17MB. `make test-go` vets the pod
build and fails when it links any `k8s.io/client-go` package.

Measured on a laptop, with `-trimpath -ldflags "-s -w"`, and the
resident memory at start of the `command`, `remote`, and `player` roles
over five runs each:

| Build | Stripped size | Resident memory at start |
|---|---|---|
| The one build before this plan | 11.5MB | 9.1 to 10.0MB |
| The full build | 28.3MB | 20.8 to 22.8MB |
| The pod build | 12.4MB | 9.7 to 10.8MB |

The pod build is 0.9MB larger than the build before this plan, which is
plan 36's quantity parser and YAML reader. The operator's own memory
request and limit in `deploy/operator.yaml` come from the build before
this plan, 17Mi at most. The full build starts about 12MB higher, so
the 64Mi limit leaves about twice its likely use, and the drill below
measures it.

## What was set aside

**Separate main packages under `cmd/`.** Each role would import only
what it needs. The program is one `main` package, and the roles share
most of it: the bus, the topics, the report, and the key tables. The
split would move most of the package into a library package. A build
tag leaves the one file out that brings client-go in.

**A hand-written election.** The operator's own client could run the
same algorithm in about 430 lines with a watch on the `Lease`, and the
binary would not grow. The organization chose client-go's election
for its operators that are not `DaemonSet`s, and one election that every operator shares is worth more
than the size.

**`strategy: Recreate`.** It stops the old pod before it starts the new
one, so a rollout never runs two copies. It prevents none of the other
two cases, and with the election in place it only adds the image pull
to every rollout's gap.

**A coordinated election.** client-go's `Coordinated` mode picks the
leader by version through `LeaseCandidate` objects. The operator has
one version in a cluster at a time, apart from a rollout, and the
rollout already prefers the new pod.

## How it was proved

`leader_test.go` runs copies against the fake API server in
`leaseserver_test.go`, which refuses a stale update with a conflict.
The tests use a 2-second `Lease`, and cover what the operator adds to
client-go:

* A step down stops the bus while the `Lease` still names the leader,
  then releases the `Lease`, and exits with no code.
* A step down whose bus session does not stop keeps the `Lease`.
* A step down releases the `Lease` when a late renewal lands after
  client-go's release read it. This test and the step-down test above
  each ran 600 times under `-race` with no failure.
* A waiting copy takes a released `Lease` in less than one duration.
* A leader that cannot renew exits with code 1.
* A leader whose `Lease` another process took exits.
* A stop while a copy waits leaves the holder alone.
* A steady leader renews with updates and no reads.

A drill on `liken-1` is owed: roll the build, scale the `Deployment`
to two replicas, confirm one copy logs `holding lease` and the other
`waiting for lease`, delete the holder's pod, and time the takeover.
Then roll a new image and time the gap in the reconcile, and read the
working set of the operator on the full build.

## A restart wakes no screen

Added on 2026-09-28, after a `liken-1` drill of a rollout. A screen
slept with its shade down and its panel dark. 0.2 s after the new
leader took the `Lease`, the shade came up. With `RollingUpdate` and
leader election, every rollout did this, so every release lit the
rooms' screens at night.

The cause was the focus marks. On each new broker session,
`reestablishRetained` published every mark the operator held. The
broker keeps retained marks across the operator's restart, so the
marks were already there, and each reader got the same mark again as
a live message. `media-screen` read any live mark that named its
`Player` as a person pointing a controller at the screen, and lifted
the shade.

The fix is on both sides:

* **The operator publishes only a mark the broker lost.** The focus
  desk records which marks the current session holds, the way the
  volume desk records the levels. `onConnect` clears the record, the
  broker's retained marks and the operator's own publishes fill it,
  and after `catchUpGrace` `reconcileFocus` publishes each mark the
  session did not deliver. After an operator restart the broker
  delivers every mark, so the operator publishes none. After a broker
  restart, which keeps no retained state (`persistence false`), the
  operator publishes each mark it holds, and each screen's new session
  reads that mark as its catch-up. `reestablishRetained` schedules a
  wake at the end of the grace, so this publish does not wait for the
  tick.
* **`media-screen` acts only on a mark that changed.** A live mark
  wakes the screen and pulses the hexagon only when it moves to this
  `Player`, or when it answers this client's own cycle request. On a
  controller that one unit lists, the operator answers a cycle with
  the same mark, and that repeat is the press's feedback, so the
  client records the cycle it asked for. Every other repeat changes
  nothing.

The operator side alone fixes the restart. The client side is there
because a screen cannot tell a person from a publisher that sends the
same mark again. For example, a broker session that delivers its
retained marks after the grace makes the operator publish a mark the
broker still holds, and the screen's own session did not restart.

| What | Test |
|---|---|
| A new session publishes no mark the broker delivered back | `TestANewSessionDoesNotRepublishAMarkTheBrokerHolds` |
| A new session publishes, once, after the catch-up, a mark the broker lost | `TestANewSessionRestoresAMarkTheBrokerLost` |
| A reconnect writes the key tables again and no mark | `TestAReconnectRewritesTheKeyTablesAndNoMark` |
| A repeat of the held mark neither wakes nor pulses | `a_mark_that_repeats_neither_wakes_nor_pulses` |
| The repeat that answers the client's cycle wakes and pulses once | `a_repeat_that_answers_this_clients_cycle_wakes_and_pulses` |
| A mark that comes back after another `Player` held it pulses | `a_mark_that_returns_after_another_player_held_it_pulses` |

The drill for this change: put a screen to sleep, roll the operator,
and confirm that the bus carries no focus mark from the new leader and
that the shade stays down. Then delete the broker's pod, and confirm
that the operator publishes each mark once, about two seconds after
it connects, and that no screen wakes.
