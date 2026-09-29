# System-pod compatibility during upgrades

Open problem. A system pod can run the new OS binary with an older pod
template during a rollout. Milestone 54 fixed the recorded missing-mount
failure. It is still an open question whether future changes to
permissions, configuration, and other template fields stay compatible.

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

## Why the binary and the template versions differ

System pods use the node-local `:installed` image tag. After an OS
upgrade, a restarted container resolves the new binary, but an existing
pod keeps its old spec. The `-002` operator needed a `/host/etc` mount
that the `-001` template did not declare.

On leaders, `init` writes the machine-operator manifest under
`/var/lib/rancher/k3s/server/manifests/liken/`, and `k3s` applies it as an
`AddOn`. A follower cannot update that file for the cluster. Moving the
cluster-operator `Deployment` onto the upgraded follower would not have
helped. Its `stewardOSPods` step replaces stale pods, but it does not
supply the new OS templates.

The follower's failure therefore blocked the leader upgrade that would
have updated the machine-operator template.

## Safeguards already implemented

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

## Remedy scope

The near-term fix is targeted compatibility safeguards. Removing the lag
between the binary and the template entirely needs a broader deployment
design. A release can keep upgrades working by tolerating known older
templates and by testing rollouts across mixed versions. Milestone 54
uses that approach.

Moving node-critical components to OS-authored static pods is a candidate
for removing the template mismatch. It changes deployment and credential
management, and it is not a selected remedy. The related
[system-image versioning problem](system-image-versioning.md) records
its scheduling constraints.

Future changes need mixed-version tests for permissions and configuration,
a follower upgrading before a leader, and leaders unable to advance.
A release that works on a single node can still fail on a fleet, as the
recorded failure shows.
