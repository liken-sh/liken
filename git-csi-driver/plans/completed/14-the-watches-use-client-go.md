# 14, The watches use client-go

Built on 2026-09-27. The numbers below come from a k3s API server in
Docker, not from liken-1. The drill that is still owed is at the end
of this plan.

## The problem

The node plugin watched two kinds through one watch loop written by
hand in `listwatch.go`: every `PersistentVolume`, for the demand
annotation of plan 10, and each writeable volume's
`PersistentVolumeClaim`, for the class of plan 05. The file was 203
lines, and its own tests were 496 lines. Seven other repositories in
the organization each had a loop of the same kind. Reviews in 2026-09
found the same faults in several of them, and each fix landed in one
repository at a time. This loop took several of those fixes, the last
at `28eec90`.

On 2026-09-27 the organization chose client-go's reflector for every
operator, in the lean form that the `operators` skill in `.agents`
records. The reason is maintenance: upstream maintains and tests the
reflector, so the project stops owning eight loops. The reference port
is `bluetooth-operator` plan 09.

## The design

### What moved

`listwatch.go` now holds `collection`, which runs one watch on
client-go's informer (`cache.NewInformerWithOptions`). `demanding.follow`
and `arming.follow` each build one, and the loop written by hand is
gone.

The driver watches through the typed clientset, not the dynamic
client that the lean form names. The lean form keeps the typed
clientset out because it links a client for every built-in kind. This
binary already links it: `events.go`, `webhook.go`, and `arming.go`
send every read and write through
`kubernetes.Interface`, and the tests run on its fake clientset. Both
watched kinds are built-in kinds. So the typed client adds no client
package, and each handler receives a `*corev1.PersistentVolume` or a
`*corev1.PersistentVolumeClaim` with no conversion step and no
conversion failure to report. The dynamic client would add its own
package and a conversion for each object. The port imports
`k8s.io/client-go/tools/cache` and nothing else new from client-go.

The `ListWatch` is wrapped with `cache.ToListWatcherWithWatchListSemantics`.
A fake clientset declares that it does not answer a streaming list,
and the wrapper passes that declaration to the reflector, so a test
reads a plain list. A real clientset declares nothing, and the
reflector reads with a streaming list.

A transform removes `metadata.managedFields` before the informer
stores an object. The `PersistentVolume` informer holds every
`PersistentVolume` in the cluster, of every driver, and the driver
never reads that field.

### What stayed

Each watch keeps the rules it had:

* **`PersistentVolume`.** Every add and every update is read. A demand
  is an annotation, and an annotation changes no generation, so the
  generation filter of the `operators` skill does not apply. `read`
  acts only on a demand later than what the volume's last fetch
  answered, so a status write acts on nothing. A delete forgets the
  handle's demand. After a gap in the watch, the informer reports a
  `PersistentVolume` it no longer holds as a tombstone, and `deleted`
  unwraps it. A tombstone that holds no `PersistentVolume` is logged.
* **`PersistentVolumeClaim`.** The list and the watch select the claim
  by name. Every add and every update is read, status writes included,
  because the resizer records the class in force on the claim's
  status. A delete is not read, so a deleted claim leaves the volume
  as it was.
* **A class that cannot be read.** The handler offers the newest copy
  of the claim to `reads`, through a channel with one slot. `reads`
  arms the volume from it. A read that fails, because the class does
  not exist yet, is made again after `defaultRetry`, with the newest
  copy offered by then. A class that arrives after the claim names it
  sends no event on the claim, so that retry is what finds it. The
  loop written by hand listed the claim again for the same reason.
* **`git_csi_watch_restarts_total`.** The `ListWatch`'s watch function
  counts every watch it opens after the first. The reflector asks the
  API server to close each watch after 5 to 10 minutes, so a healthy
  watch counts 6 to 12 restarts an hour. The metric's comment says so.

The reflector's own messages go to klog on standard error, as they do
in `bluetooth-operator`. A list that the API server refuses is one of
them, so the test that read a refused list from the driver's log is
gone. The search for the claim through the `PersistentVolume`s
(`claimOf`) is one list, not a watch, and did not change. The
ClusterRole already grants `list` and `watch` on both kinds, and a
streaming list uses the `watch` verb, so the node's RBAC does not
change.

The module already required client-go v0.36.3. `go mod tidy` added
one indirect line, `github.com/pmezard/go-difflib`. liken's
`k3s/VERSION` names v1.36.4+k3s1, so the minor matches. This plan
changes no pin.

### The reflector and the organization's guards

The reflector does not meet the three guards in the `operators` skill
exactly. `bluetooth-operator` plan 09 lists the five differences from a
reading of client-go v0.36.3's `tools/cache/reflector.go`. None of them
loses an event, and the organization accepts them. The loop written by
hand waited 30 seconds after a failed watch; the reflector's backoff
starts at 800 ms and grows to 30 seconds.

### The controller and leader election

The controller Deployment runs two containers. The driver container
writes to the cluster: it marks a `PersistentVolume` for each webhook
it accepts, and marks every one it matches when it starts. Each write
answers one request, and a mark is a timestamp that the node plugin
compares by time. Two replicas that mark the same volume cost at most
one more pull, so the driver container needs no leader. It reconciles
nothing, and client-go leader election in it would stop a second
replica from answering webhooks that the Service sends it.

The resizer sidecar reconciles. It watches the claims and writes each
claim's status after `ControllerModifyVolume`. Two resizers race on
that status. `deploy/controller.yaml` now runs it with the sidecar's
own `--leader-election`, which holds a Lease named
`external-resizer-git-liken-sh` in the controller's namespace. A Role
grants `get` and `update` on that one name and `create` on leases. The
Deployment names `RollingUpdate` with `maxSurge: 1` and
`maxUnavailable: 0`, which the default already was for one replica.
`replicas` stays 1, and its comment says 2 is safe.

The Lease lasts 30 seconds, with the sidecar's 10-second renew
deadline and 5-second retry. A leader's renew deadline starts one
retry period after its last renew, so a leader that cannot renew gives
up as late as 15 seconds after that renew. It then exits, and its
workers can still write until the process ends. The sidecar's default
15-second Lease would let the next leader start at that same moment,
so the manifest sets 30 seconds.
The resizer does not release the Lease when it exits,
because its `ReleaseLeaderElectionOnExit` gate is alpha and off. So a
rollout leaves class changes waiting for up to 30 seconds.

client-go leader election is not fencing. A resizer that paused while
it held the Lease can finish a status write it already sent after the
next leader starts.

### The binary and the node plugin

The node plugin is a DaemonSet of the same binary, under its `node`
subcommand. The organization does not accept leader election's cost
for a DaemonSet role. This port adds no leader election to the binary,
so no role pays for it. To answer whether one binary could carry it,
a build of this change with `leaderelection` and `resourcelock` linked
and reachable was measured beside it. Because the typed clientset is
already linked, it adds 57 KB and no measurable RSS.

## Measurements

Each binary ran its `node` subcommand in a container, on the laptop,
against a k3s v1.36.3+k3s1 API server in Docker, with a ServiceAccount
token mounted where `rest.InClusterConfig` reads it. The binaries were
built as the Dockerfile builds them (`CGO_ENABLED=0`, `-trimpath`,
`-s -w`). The `PersistentVolume`s were static volumes of `git.liken.sh`
with a demand annotation, and none was staged on the node. The RSS is
`VmRSS` from `/proc/1/status` 45 seconds after the start, in two runs
of each build.

| | Hand-written loop | client-go | client-go, leader election linked |
|---|---|---|---|
| Stripped binary | 48,402,592 bytes | 48,861,344 bytes | 48,918,688 bytes |
| Linked Go packages (`go list -deps .`) | 669 | 682 | 803 |
| RSS, idle, no `PersistentVolume`s | 33.6 to 33.9 MB | 36.7 to 37.1 MB | not measured |
| RSS, idle, 250 `PersistentVolume`s | 36.4 to 36.5 MB | 40.0 to 40.1 MB | 39.3 to 40.0 MB |

The port adds 459 KB of binary, 13 packages, and about 3.5 MB of RSS.
The 13 packages are `tools/cache` and what it imports:
`tools/pager`, `util/watchlist`, `util/consistencydetector`, and the
`apimachinery` packages for internal list options. The leader election
build adds 121 packages, most of them `k8s.io/client-go/informers`
and `k8s.io/client-go/listers`, which the linker drops almost whole
because nothing calls them. On the
hand-written build the same two imports measured 36.0 to 36.6 MB at
250 volumes, the same as without them.

## Tests

The tests of the loop's own faults are gone, because the reflector is
upstream's to test. The tests that remain run the driver's handlers
through the real reflector against the fake clientset, whose watches a
test scripts with `watch.FakeWatcher`. The informer hands a handler
its events after the fake watch returns, so a test that sends an
event waits for its outcome: `settled` sends a demand on a volume no
test stages and waits until the node reads it, and the informer keeps
the order of events.

| What | Test |
|---|---|
| A demand written between the list and the watch pulls the tree | `TestADemandWrittenBetweenTheListAndTheWatchPullsTheTree` |
| A delete the watch sends, and a list that no longer holds the volume, forget its demand | `TestADeletedPersistentVolumeTakesItsDemandAway`, `TestAListThatNoLongerHoldsAPersistentVolumeTakesItsDemandAway` |
| A tombstone with no copy is logged | `TestADeleteWithNoCopyOfThePersistentVolumeIsLogged` |
| A deleted claim leaves the volume armed | `TestADeletedClaimLeavesTheVolumeArmed` |
| A newer claim replaces one whose class could not be read | `TestANewerClaimReplacesOneThatFailedToRead` |
| The reads end with the driver during a retry | `TestTheReadsEndWithTheDriverWhileAReadFails` |
| A watch that closes counts on `git_csi_watch_restarts_total` | `TestARestartedWatchCountsOnGitCSIWatchRestartsTotal` |
| The informer stores no `managedFields` | `TestTheInformerStoresNoManagedFields` |

`TestADemandReadWhileTheVolumeStagesIsActedOnWhenTheStageEnds` failed
once in CI at `82a625a`: the test returned while the pull its demand
started was still writing into the store, and the cleanup that removes
the store found a directory that was not empty. The driver stops that
pull when its context ends. Nothing waited for it to stop, so the
fault was in the test. The test now waits until the pass has ended,
by taking the repository's lock that the pass holds.

## Considered and set aside

* **The dynamic client.** It is the lean form's client, and it would
  add its own package and a conversion step to a binary that already
  links the typed client for the same two kinds.
* **`informers.NewSharedInformerFactory`.** The typed informers link
  nothing new here, but a factory starts shared informers that this
  driver does not share, and `NewInformerWithOptions` is the
  reference's shape.
* **client-go leader election in the controller's driver container.**
  It has nothing to guard, and it would stop a second replica from
  answering webhooks.

## The drill still owed

On liken-1, with the driver on a build of this change:

1. Read the node plugin's working set on a 1 GB machine, and compare
   it with the build before this change.
2. Annotate a read-only claim's `PersistentVolume` and time the pull.
3. Change a writeable claim's class and check that the volume arms
   with the new class.
4. Roll the controller Deployment and check that one resizer holds
   `external-resizer-git-liken-sh` and that a class change after the
   rollout arms the volume.
