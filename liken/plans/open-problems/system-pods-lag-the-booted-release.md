# System pods lag the release a machine booted

Open problem, and an open design question. A system pod's image and
its pod template can each differ from the release that its machine
booted. During a rollout, a system pod can run the new OS binary with
an older pod template. Milestone 54 fixed the recorded missing-mount
failure. It is still an open question whether future changes to
permissions, configuration, and other template fields stay compatible.

System pods use the node-local `installed` image tag, so each node runs
the images imported by its own OS. Replacing that tag with
version-specific references needs a design that still works when the
fleet runs several releases. An implementation using version-stamped
references was reverted on 2026-08-26, after a review found three ways
it could block a rollout. The constraints below record that review.
They do not select a replacement.

Both halves point to one candidate: OS-authored static pods for the
node-critical components. The candidate is not a selected remedy.

## The recorded failure

A multi-machine cluster upgraded from `2026.08.12-001` to
`2026.08.12-002`. The conductor granted a follower the first turn. It
returned on `-002` with `HostEntriesApplied=False` and this error:

```text
writing /host/etc/hosts: open /host/etc/hosts.tmp-…: no such file or directory
```

The machine declared no `hostEntries`. Its phase became `Degraded`, so
the conductor granted no further turns. One machine ran `-002` while
the others remained on `-001`.

The same release worked on a single-machine cluster because that machine
was also the leader responsible for publishing the new template.

## How system pods get their images now

On boot, `seedClusterState` supplies the running release's image tarballs
under `k3s/agent/images`. Importing them updates `installed` on that
node. A container restart resolves the tag against the local image store,
so no cluster-wide template update has to name the new build.

The intended behavior is that a node-bound pod runs its own node's OS
build by the next boot, without version-aware scheduling. This supports
rollouts across mixed versions. There is still a startup window: a
container that starts before import finishes can resolve the previous
build. The mutable tag in the pod spec does not identify which build was
selected. Runtime image IDs and the version tags in the local store
still show which build runs. The problem is that the spec does not pin
the running build.

## Why the binary and the template versions differ

After an OS upgrade, a restarted container resolves the new binary from
the node-local `:installed` tag, but an existing pod keeps its old spec.
The `-002` operator needed a `/host/etc` mount that the `-001` template
did not declare.

On leaders, `init` writes the machine-operator manifest under
`/var/lib/rancher/k3s/server/manifests/liken/`, and `k3s` applies it as an
`AddOn`. A follower cannot update that file for the cluster. Moving the
cluster-operator `Deployment` onto the upgraded follower would not have
helped. Its `stewardOSPods` step replaces stale pods, but it does not
supply the new OS templates.

The follower's failure therefore blocked the leader upgrade that would
have updated the machine-operator template.

## Safeguards already implemented

Milestone 54 built these safeguards in d11a8a6c.
[Milestone 54](../completed/54-system-pod-template-lag.md) records the
implementation and lab results.

- `ownPodIsStale` in [staleness.go](../../machine-operator/staleness.go)
  compares the pod's `liken.sh/os-version` annotation with the running OS
  version. `hostEntriesCondition` in
  [conditions.go](../../machine-operator/conditions.go) classifies a
  missing-path error from a stale pod as `AwaitingPodRefresh`. This
  produces `UpdatePending`, and the rollout continues. It applies
  whether or not host entries were declared. Entries that the operator
  has not observed are not reported as applied.
- `decideRollout` in [rollout.go](../../cluster-operator/rollout.go)
  compares the applied `DaemonSet` version with the target. While they
  differ, it holds followers when an eligible leader waits for a turn, or
  when a leader has a grant that it has not used yet. A leader upgrade
  can then update the template before more followers reboot. The usual follower-first
  ordering remains when this exception does not apply.

The existing `liken.sh/machine=true` node label does not identify an OS
version, so scheduling cannot use it to select a version.

## Manual recovery before milestone 54

Before these safeguards shipped, recovery needed a patch to the running
`DaemonSet` with the new volume and mount, copied from the new release's
template. The `AddOn` reapplied its template when the file checksum
changed, so the patch stayed in place until a leader booted the new
release.

With `OnDelete`, changing the template did not replace existing pods.
Deleting the affected follower's pod recreated it with the new mount.
The machine became `Ready` within seconds and the rollout resumed. Other
machines needed no manual deletion. `stewardOSPods` refreshed them after
their upgrades.

This section records the recovery used for that incident. Milestone 54
covers the host-entry case, so it no longer needs this recovery.

## Template changes the safeguards do not cover

The missing-path classification covers only missing paths. It does not
check template compatibility in general. A missing permission or
environment variable can fail in a different way and still block the
operator. The resulting condition depends on the failing code path, and
it is not always `ApplyFailed`.

Leader-first ordering also needs a leader able to take a turn. If leaders
are still staging, are on `Manual` without approval, or are unavailable,
a follower can still run a newer binary with an older template.

## Why version references can block a rollout

With `image: liken.sh/<name>:<version>` and `imagePullPolicy: Never`,
a node cannot start a pod whose named image is absent. The review of
the reverted implementation found these paths:

- The `cluster-operator` `Deployment` uses node selection without an
  OS-version constraint. After an upgraded leader applies the new
  template, its replacement pod can be scheduled on an older node and
  fail with `ErrImageNeverPull`. The rolling update keeps the old pod
  while the new one fails, so the old pod keeps the leader election
  `Lease` and conducts the rollout. When the old pod's node drains for
  its own turn, no copy runs, and other machines receive no new upgrade
  turns.
- [init/imports.go](../../init/imports.go) discards the container store
  after an import boot that the operator never proved. The rebuilt store
  may contain only the current release's images. An older machine-operator
  pod spec then cannot start. That stops its heartbeat, its
  reconciliation, and its proof that the imported images work. The
  missing proof can cause another discard on the next boot.
- `OnDelete` keeps existing `DaemonSet` pods, but it does not keep an
  older template for a node that needs a new pod. A machine joining
  from an older stick, or recreating an evicted pod, receives the current
  template even if its OS has only older images.

## Constraints on version labels and affinity

The review considered a node label naming the booted release and
required node affinity on system pods. It recorded two upstream findings:

- `k3s` patches `--node-label` values onto the `Node` on each agent start.
  The review checked `pkg/agent/run.go` on release branches 1.31 through
  1.34. So `init` can supply the label without waiting for the machine
  operator. The agent documentation examined at the time said the labels
  apply only at registration, which the code does not match.
- The Kubernetes `DaemonSet` controller's `updateNode` path deletes pods
  when a node stops matching required affinity. After slot fallback, a
  version label can move backward and trigger deletion of log relays or
  `iscsid`. `OnDelete` does not prevent deletion caused by affinity.

Required affinity may suit the movable cluster-operator `Deployment`.
Applied unchanged to every `DaemonSet`, it does not meet the existing
fallback requirements. Recheck these upstream observations against the
vendored versions before implementation.

## The static-pod candidate

An OS-authored kubelet static-pod manifest could keep the node-critical
machine operator's image and pod configuration matched to the booted OS.
Then its image would not come from a template that another node updated,
and the template mismatch above would go away. This is one candidate,
and other designs remain open. It changes deployment and credential
management.

Static pods cannot use the normal pod `ServiceAccount` projection path.
A client certificate is one candidate credential. Issuance, rotation,
revocation, and authorization would need to work before this operator
starts. The related [node-scoped credential problem](machine-operator-credentials-have-fleet-wide-access.md)
needs both a node identity and enforcement of that identity's authority.
A different certificate with no other change does not isolate nodes.

[image/oci.sh](../../image/oci.sh) already creates a versioned tag beside
`installed`. Until a replacement is designed, that tag helps a person
inspect which build runs, and the running pod templates stay the same.

## Remedy scope

The near-term fix is targeted compatibility safeguards. A release can
keep upgrades working by tolerating known older templates and by
testing rollouts across mixed versions. Milestone 54 uses that
approach.

Removing the lag between the binary and the template entirely needs a
broader design for deployment, scheduling, and credentials. A
node-critical operator must still start from its own OS during upgrade
and fallback, and the remedy must keep that behavior while it changes
how images and manifests are selected.

The decisions include which components should be static pods, how the
movable cluster operator finds compatible nodes, and how credentials are
bootstrapped and scoped. A changed tag string or added required affinity
does not answer these questions.

## Verification needed

Future changes need mixed-version tests for permissions and configuration,
a follower upgrading before a leader, and leaders unable to advance.
A release that works on a single node can still fail on a fleet, as the
recorded failure shows.

Any candidate must cover the three failure paths in
[Why version references can block a rollout](#why-version-references-can-block-a-rollout),
fallback to an older slot, and container startup before import
completes. Test loss and rotation of the proposed credentials as well.
A solution must not depend on the machine operator already running to
make its own image or initial credential available.
