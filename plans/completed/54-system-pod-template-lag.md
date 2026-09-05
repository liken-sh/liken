# The system-pod template lag

A rollout must survive the gap between a system pod's binary and the
pod spec around it.
[The open problem](../open-problems/system-pod-template-compatibility.md)
records the failure: a release added a `/host/etc` mount to the
machine-operator template, a follower took the first turn, and its new
binary failed writing `/etc/hosts` through a mount its old pod did not
declare. The machine went Degraded, the conductor stopped granting
turns, and the rollout held with one machine on the new release.

The lag itself is by design. System pods run the stable image tag
`:installed`, their DaemonSets update on `OnDelete`, and only a
leader's boot rewrites the AddOn manifests. A follower that reboots
first always runs the new binary inside the old pod spec for a while.
The lag is not the failure. The machine operator reported the lag as a
fault, and the conductor let every follower enter the lag before any
leader could end it.

This milestone builds two halves. The guard makes the lag a reported,
expected state instead of a fault. The gate makes the conductor send
a leader first while the applied template is behind the target, so
the lag stays short.

## The guard: the lag is not a fault

The machine operator detects its own stale pod spec, and classifies
failures differently while it has one. The signal costs
nothing new. The pod template stamps the `liken.sh/os-version`
annotation on every pod, the facts say which release this machine
runs, and the operator already holds `pods: list` for the drain. Once
per pass, the operator finds its own pod, the one
`app: liken-machine-operator` pod on its node, and compares the
annotation against the running release. A difference means the pod
spec predates the binary. When the read fails, the operator treats
the pod as current, so an API error never masks a real fault.

While the pod is stale, one rule applies: an actuation failure whose
error is `fs.ErrNotExist` reports the reason `AwaitingPodRefresh`
instead of `ApplyFailed`, and the phase table maps that reason to
`UpdatePending`. A path that does not exist inside a stale pod means
a mount the old template lacks, whatever the mount is, so a release
that adds any future mount gets this behavior for free. The machine
reads as mid-change and not as unwell. An update to its pod is
pending, and the pod steward delivers it seconds after a leader boots
the release. The conductor keeps granting turns, and the
fleet sweep keeps the Cluster out of `Degraded`.

For host entries, `status.hostEntries` stays empty while the mount is
missing. The status reports what the operator observed, and without
the mount it observed nothing. The condition's message says the pod's
template predates this release and names the steward's refresh as the
fix.

Three precedents shape this half. The DRA plugin already tolerates a
mount the old template lacks (`machine-operator/main.go`), because
dying would kill the status publishing the pod steward waits on.
`sysctlsCondition` already keeps a machine `Ready` when the fault
belongs to the release rather than to the machine. A per-machine
health signal that a whole fleet trips at once gives no information.
And the reason must not be `AwaitingTurn`, because the
conductor scans conditions for that reason and would read the guard
as a staged change.

The guard covers every mount the template may ever grow. It does not
cover an RBAC rule or an environment variable the new binary needs,
because those fail as a 403 or a bad value, not as a missing path.
The gate keeps those lags out of the common path, and the downgrade
rule can widen if a real case appears. The cluster operator has the
same lag window and stays out of this milestone. Its Deployment
recreates its own pod when new manifests apply, and its conditions
land on the Cluster, which the conductor never blocks on.

## The gate: a leader goes first while the template lags

The conductor grants workers first, then leaders, because a worker
mistake costs little while every leader holds a share of quorum.
That order stays the default. The gate adds one exception: while the
applied system-pod template is behind the fleet's target release, the
first turn goes to a leader, because only a leader's boot can advance
the template, and every follower that reboots before then extends the
lag instead of ending it.

The signal already exists and costs nothing new. The
`liken-machine-operator` DaemonSet has the `liken.sh/os-version`
annotation naming the release whose manifests are applied, and the
pod steward already reads it every sweep (`cluster-operator/steward.go`).
The fleet sweep reads it once and passes it to `decideRollout`, which
compares it against `spec.version`:

* The annotation differs from the target, and a leader either awaits
  a turn or holds an unspent grant: the leader goes first, and no
  waiting worker is granted in that sweep. The hold lasts through the
  leader's whole turn, because each sweep recomputes it from the
  grant that is still outstanding, and the `Progressing` message says
  what the workers wait for as long as the hold lasts.
* The annotation differs from the target, and no leader awaits a turn
  and none holds a grant, because every leader's `rebootPolicy` is
  `Manual`, every leader is still staging, or a leader is down
  without a grant: workers proceed in today's order. The gate must
  not stall the rollout, and the guard covers the followers through
  the lag.
* The annotation matches the target, or is empty because no DaemonSet
  exists yet: today's order, workers first.

The gate is self-clearing. The first leader that boots the target
release rewrites the manifests, k3s applies them, the annotation
advances, and the rest of the rollout runs workers-first as before.
On a single-machine cluster the one machine is a leader and nothing
changes. No new RBAC: the cluster operator already holds `get` on
DaemonSets.

## What was considered and set aside

* **A template digest in the release document.** The channel could
  say whether a release changes the template, so the gate only fires
  when it must. This spreads one decision across the release schema,
  the release build, `image/build.sh`, and a new fetcher in the
  cluster operator, to spare one leader-first turn per version
  rollout. The gate's cost is too small to buy back at that price.
* **The cluster operator applies the templates itself.** The flux
  planter proves the shape, but the wrong actor holds the bytes: the
  cluster-operator pod runs whatever build is on its node, so
  during a wedge it may hold the old template. It would also
  duplicate the image's manifest seed and put a second writer on an
  object the k3s AddOn owns.
* **Always leader-first for version rollouts.** Fixes the class, but
  stops worker-first proving on every release forever, when most
  releases change no template.

## The drill

The load-bearing assumption is that one upgraded leader advances the
applied template. Each leader holds its own copy of the manifests
file, and k3s tracks one applied checksum per AddOn, so leaders on
differing releases could contend for the applied state. The drill
that settles it: a cluster with two leaders and one worker, upgraded
through a release that changes the template. Watch the DaemonSet's
`liken.sh/os-version` annotation after the first leader boots, and
watch whether it holds or oscillates while the second leader is
behind.

The second drill replays the original failure: a multi-machine
cluster on a release before the `/host/etc` mount, upgraded to one
after it. The rollout must complete without a hand patch, the
follower must report `AwaitingPodRefresh` and `UpdatePending` while
its pod is stale, and the steward must refresh the pod after the
leader's boot.

## What the lab measured

The lab ran both drills on 2026-08-12, on the five-machine dev fleet
(three leaders, two workers), re-founded on public 2026.08.12-001.

The replay completed in 2 minutes 54 seconds with no hand patch. The
old conductor granted a worker first, the worker came back on the lab
release inside its old pod, and it reported `HostEntriesApplied`
`False` with reason `AwaitingPodRefresh` and phase `UpdatePending`.
The conductor kept granting turns. The first leader's boot advanced
the DaemonSet annotation, the steward recreated both workers' pods
within 8 seconds of it, and the stale window on the first worker
lasted about 60 seconds.

The applied template held with no oscillation, in four rollouts. The
widest window left two leaders on the old release for about 50
seconds after the first leader wrote the new manifests, sampled every
10 seconds; the annotation never moved backwards.

The gate drill granted the leader first, the exact reverse of the
replay's order, and finished in 2 minutes 30 seconds. No machine
reported `AwaitingPodRefresh`, because every follower rebooted into a
template that was already current.

The drill also exposed a flaw in the gate's first build: it held
workers only in the sweep that granted the leader. The grant's own
status write wakes the next sweep within milliseconds, that sweep
reads the granted leader as busy, and the hold and its message were
both rewritten away. With the default budget of one machine this cost
nothing. A budget of two would have granted a worker during the
leader's turn, into the lag the gate exists to prevent. The message an
operator could observe survived under a second. The hold
now keys on durable state, a leader that can be granted or a leader
whose granted turn still runs, so it lasts the whole turn. The gate
stays best-effort in one case: while every leader is still staging,
no leader waits and none holds a grant, so a fast-staging worker can
enter the lag. The guard covers that case.

A second drill verified the durable hold, at a budget of two, on a
fleet whose conductor already ran the fixed code. The leader was
granted alone, no worker held a grant while the annotation lagged,
and the first worker grant landed in the same sample where the
annotation advanced. The message clause held verbatim across the
leader's whole 30-second turn, sampled every 5 seconds. Every
waiting machine read `Ready` `False` with reason `UpdatePending`,
and reason `Degraded` never appeared. The verification needed two
rollouts by nature: the rollout that delivers a conductor fix is
still conducted by the old release, so the fix only governs the
rollout after it.

## The manual

The troubleshoot guide's "The fleet stays on the old version" list
gains one entry: `AwaitingPodRefresh`, the machine runs the new
release and waits for its operator pod to be recreated from the new
template, which happens after a leader boots the release.
