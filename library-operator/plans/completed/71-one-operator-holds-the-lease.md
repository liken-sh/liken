# 71, One operator holds the Lease

Built on 2026-09-27. The operator runs client-go's leader election
(`k8s.io/client-go/tools/leaderelection`) with a `LeaseLock` on the
`coordination.k8s.io/v1` `Lease` named `library-operator` in its own
namespace. Only the copy that holds the `Lease` opens a bus session,
watches, and reconciles. On `SIGTERM` the leader finishes its pass,
stops its watches and its bus session, and releases the `Lease`, and a
waiting copy takes it on its next read, within about 11 seconds. The
`Deployment` rolls with `RollingUpdate`. The design and most of the
code follow `media-operator` plan 37. The pods and `Jobs` the operator
creates run a second build of the program, with no client-go in it.
The drill on `liken-1` is owed, at the end of this plan.

## The problem

The operator must run as one copy. It creates and deletes `Jobs`, pods,
and claims, and writes the status of every `Library` and `Catalog`, so
two copies that reconcile at the same time race: both create a `Job`
for one scan, or one deletes the screen pod the other just made. The
`Deployment` set `replicas: 1` and `strategy: Recreate`. `Recreate`
stops a rollout from overlapping, but a node partition, or a replica
count that a person sets to 2, still runs two copies. A guard in the
operator's code applies whatever the spec says.

## The design

**client-go's election.** `leader.go` builds a `LeaderElector` with a
`resourcelock.LeaseLock` over the typed coordination client. The
identity is the pod's name and a random suffix, so a restarted
container is a new candidate. `operate` names the pod from `POD_NAME`
and the namespace from `OPERATOR_NAMESPACE`, which the `Deployment`
already sets, and fails when either is unset.

**What waits for the `Lease`.** The metrics listener and the webhook
server start in every copy, before the `Lease`. Everything else waits:

* The bus session. Two copies under the one client id
  `library-operator` would end each other's sessions at the broker.
* The watches. A copy that waits reads nothing from the API server.
* The passes.

The `Service` sends a webhook to either copy. A waiting copy holds each
path it receives, as the leader does, and its first pass after it takes
the `Lease` creates the `Jobs`. With `replicas: 2`, about half the
webhooks wait for a failover, so the manifest keeps one replica.

**The timings.** The `Lease` lasts 30 seconds, the renewal deadline is
10 seconds, and the retry period is 5 seconds. After its last renewal
at T, the leader tries again at T+5s and gives up at T+15s. client-go
then tries to release the `Lease`, bounded by one more renewal
deadline, and only then calls `OnStoppedLeading`, so the leader exits
by T+25s. A waiting copy takes the `Lease` 30 seconds after it saw the
last renewal, after T+30s. With client-go's 15-second duration the two
times would meet, and a new leader could start while the old one can
still write. A waiting copy takes a released `Lease` within about 11
seconds, and an abandoned one 30 to 41 seconds after the last renewal.

**A loss ends the process.** `OnStoppedLeading` exits with code 1,
because a pass in flight must not keep writing after another copy can
take the `Lease`. This is the one exit that is not `main`'s. The kubelet
restarts the container, and it waits as a new candidate.

**The election is not fencing.** A leader that pauses, for example on a
stalled node, can resume after its `Lease` expired and finish a request
it had already sent, while another copy leads. The API server's
`resourceVersion` checks refuse a stale status write and a stale
finalizer patch, and a duplicate create meets a conflict, but a delete
in flight can still land.

**A shutdown releases the `Lease`.** `ReleaseOnCancel` is on. On
`SIGTERM` the loop finishes its pass, and `stepDown` stops the watches,
stops the bus session, and waits up to 5 seconds for the session to
end. Then it ends the election. A renewal that was already sent can
reach the API server after client-go's release read the `Lease`, and
the release's update then conflicts. So `end` reads the `Lease` again
and clears it itself when it still names this process, up to three
times within 5 seconds, the fix `media-operator` made in `b65b006`.
When the bus session does not end in time, the process exits holding
the `Lease`, and a waiting copy takes it when it expires.

**The grace period.** A shutdown finishes the pass in flight, up to
30 seconds, waits up to 5 seconds for the bus session, and releases the
`Lease`, up to 16 seconds. The pod's `terminationGracePeriodSeconds` is
60, so the kubelet does not kill the operator before the release.

**The rollout.** The `Deployment` uses `RollingUpdate` with `maxSurge:
1` and `maxUnavailable: 0`. The new pod starts and waits for the
`Lease`, and the old pod stops once the new one runs, so the image pull
happens while the old pod still leads. `replicas` stays 1.

**Permissions.** A `Role` in the operator's namespace grants `get` and
`update` on the one `Lease` by name, and `create` on leases, because
RBAC cannot read a name off a create.

## The pod build

client-go's `resourcelock` package imports the whole typed clientset
and its scheme. The organization accepts that cost for the operator,
one pod for each cluster, and not for the roles of the same program
that run in the pods and `Jobs` the operator creates: the scanner and
the enricher in every library `Job`, the confirmer and the reporter in
every catalog pod, the progress agent, and the Jellyfin roles. So the
program has two builds:

* The full build, `/library-operator`, which runs the operator and
  every role.
* The pod build, `/library-operator-pod`, built with the tag `pod`.
  `operate_pod.go` replaces `operatorprocess.go` in it and refuses to
  run the operator. `watch.go` and `leader.go` are not in it, so it
  links no client-go at all.

Every pod and `Job` the operator creates runs `/library-operator-pod`.
The operator image carries both builds, and the ffmpeg image carries
the pod build alone. `make test-go` vets the pod build and fails when
anything in it links client-go.

A `Job` that the release before this one created names
`/library-operator`. On the operator image that path is still the full
build, which runs every role. The ffmpeg image no longer carries it, so
a probe or trickplay container of such a `Job` that starts again after
a cluster pulls the new ffmpeg image fails, and the `Job` fails. That
happens only where the image tag moves under a running `Job`, as
`:latest` does, and the operator creates the `Job` again from the new
template after the backoff of a failed `Job`.

The pod build's RSS is not measured. Its binary is the size of the
binary before this plan, and it links the same 265 packages.

| | Before | After |
|---|---|---|
| The operator's stripped binary | 12.9 MB | 30.8 MB |
| The operator's steady RSS | 19.3 MB | 31.3 MB |
| The pod build's stripped binary | 12.9 MB (one build) | 11.9 MB |
| The pod build's linked packages | 265 (one build) | 265 |

The operator's numbers include plan 70's watches. The RSS is the steady
`VmRSS` of the operator against a k3s v1.36.3 API server in Docker.

## How it is proved

`leader_test.go` runs the election against the fake `Lease` server in
`leaseserver_test.go`, with a 2-second `Lease`. It proves the order of
a shutdown, the exit on a lost `Lease`, the exit when another process
takes the `Lease`, the prompt takeover a release gives a waiting copy,
and a steady leader that renews with no read.
`TestAStepDownReleasesTheLeaseAfterALateRenewal` lands a renewal after
client-go's release has read the `Lease`, and passed 200 times in a row
under `-race`. `TestOperateRunsUntilTheStopSignal` runs the whole
process against the fake cluster: it holds the `Lease` while its first
pass runs, and leaves the `Lease` released after `SIGTERM`.
`TestACopyThatWaitsForTheLeaseActsOnNothing` checks that a waiting copy
sends no request and opens no watch. The build after the port also ran
against a k3s API server in Docker, took the `Lease`, and reconciled.

## The drill that is owed

On `liken-1`, after the main session rolls the build:

1. Watch the rollout: the new pod logs that it waits for the `Lease`,
   the old pod stops, and the new pod logs that it holds the `Lease`
   within about 11 seconds.
2. Scale the `Deployment` to 2, check that the second pod waits and
   that only one pod writes, then scale it back to 1.
3. Check that a catalog pod and a library `Job` created after the roll
   run `/library-operator-pod`, and compare their memory with the pods
   from before.
