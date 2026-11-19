---
title: Roll back
weight: 50
description: "Return a machine or the fleet to a version that works: the automatic fallback after a failed first boot, and the deliberate rollback that points the Cluster at an earlier release. Use when an upgrade fails or misbehaves."
---

# Roll back

When a new version fails its first boot, the machine returns to the
previous version on its own. To move the fleet back to an earlier
version on purpose, point the Cluster at that version.

## The automatic fallback

Every machine has two boot slots, A and B. The machine runs from one
slot and writes a downloaded release into the other. An upgrade boots
the new slot once, as a trial:

* If the new kernel panics, the machine resets, and the firmware boots
  the proven slot. No `liken` software runs for this.
* If the new version boots but does not rejoin the cluster within ten
  minutes, a watchdog reboots the machine, and it starts again from
  the proven slot.

In both cases, the machine runs the version it ran before. Its phase
is Blocked, its `VersionConverged` condition has the reason
`RejectedLastBoot`, and
[`status.boot.systemRejection`](/docs/reference/machine/#statusbootsystemrejection)
records what happened. The machine does not try the failed release
again until you point
[`spec.version`](/docs/reference/cluster/#spec--version) at a
different version.

A release is never republished under the same version. To fix a bad
release, publish a release with the next serial number, add it to the
catalog, and point `spec.version` at it.

## Deliberate rollback

To move the fleet back to an earlier release, point `spec.version` at
that release:

    kubectl edit cluster

The version must still be in
[`spec.releases.catalog`](/docs/reference/cluster/#specreleasescatalog),
so keep old entries in the catalog. A rollback runs like an upgrade:
each machine downloads the older release into its inactive slot,
verifies it, and reboots when it gets its reboot turn. The `default`
[`MachineDisruptionBudget`](/docs/reference/machinedisruptionbudget/)
allows one machine at a time unless you raised its limit. The cluster
keeps serving during the rollback.

### The oldest release you can roll back to

The API refuses an edit that sets `spec.version` to a release older
than `2026.09.28-001`. A rollback runs `cluster-operator` with the
RBAC of the target release, and `2026.09.28-001` is the first release
whose RBAC lets `cluster-operator` create and update its
leader-election `Lease`. Only the replica that holds the `Lease`
grants reboot turns. Under an older release's RBAC, no replica could
take the `Lease`, no machine would get a reboot turn, and the rollback
would stop.

A `Cluster` that already has an older version keeps it. The API
checks the rule only when an edit changes `spec.version`.

A release from before
[`status.rebootTurns`](/docs/reference/cluster/#statusrebootturns)
does not record its grants in that list. While it runs, its
`cluster-operator` writes the Cluster's status without the field, so
the list is empty. Without the list, two replicas of
`cluster-operator` can grant turns from the same budget slot. This
happens when the replica that holds the `Lease` pauses, another
replica takes the `Lease`, and the first replica resumes and writes a
grant. So more machines can reboot at once than the budget allows.
When you roll forward, the newer
`cluster-operator` adds an entry for each grant of the older release
before it grants a new turn.

A release from before the
[`Disruption`](/docs/reference/disruption/) and
[`MachineDisruptionBudget`](/docs/reference/machinedisruptionbudget/)
kinds reads neither of them. Its `cluster-operator` limits the
rollout only with
[`spec.disruption.maxUnavailable`](/docs/reference/cluster/#specdisruption),
and its `machine-operator` ignores `Disruption`s. So before you roll
back to such a release, set `spec.disruption.maxUnavailable` to the
limit you want. The current release ignores that field, so the edit
has no effect until the older release runs. The older release ignores
`MachineDisruptionBudget`s, so a budget with a selector has no effect
until you roll forward again. `liken request-reboot` and
`liken approve-reboot` only write `Disruption`s, so while the older
release runs, use that release's `liken` CLI to request or approve a
reboot.

### Before you roll back past a spec field

A release later than `2026.10.08-002` can boot a proven manifest that
has fields it does not know. It ignores those fields, and the console
lists them, for example `the proven manifest names fields this release
does not know, and this boot leaves them out: spec.serio`. The
machine keeps its storage, network, and every other field. That
release's operator also removes conditions that only a newer release
writes, such as `SerioAttached` and `ClockSynchronized`, so an
outdated condition does not make the machine `Degraded`. You can roll
back to such a release without the steps below.

A release up to and including `2026.10.08-002` refuses a manifest
that has a field newer than the release. `spec.serio` is such a
field. When a machine that declares `spec.serio` boots such a release,
the release cannot use the proven manifest, and it falls back to the
seed manifest, the machine's manifest from install time, which the
release's image on that slot has:

* If the image has a seed manifest for the machine, the machine boots
  with it. Its storage and network can be out of date, and the boot
  records it as the new proven manifest.
* If the image has no seed manifest for the machine, the machine boots
  with an empty `Machine`.
* If the seed manifest also declares `spec.serio`, the older release
  cannot read it either, and the machine powers off. To recover it,
  you need a new install stick.

To roll such a machine back to a release up to and including
`2026.10.08-002`, remove the field first:

1. Remove `spec.serio` from the `Machine`:

       kubectl patch machine <name> --type=json \
         -p '[{"op":"remove","path":"/spec/serio"}]'

2. Wait until the `SpecConverged` condition shows that the removal is
   staged. The reason tells you what the machine waits for:
   * `RebootPending`: the machine has `rebootPolicy: Manual` and waits
     for your approval. Run `liken approve-reboot <deployment-dir>
     <name>`.
   * `AwaitingTurn`: the machine waits for a reboot turn. This happens
     under both policies.
   * `RebootRequested`: the machine has its turn and reboots now.

   Then wait until the condition is `True` after that reboot. The
   serial line stays attached until the boot that applies the
   removal.
3. Point `spec.version` at the older release. The older release boots
   the staged manifest, or the proven manifest that the reboot in step
   2 promoted, and it can read both.

The older release does not know the `SerioAttached` condition, so it
leaves the condition as it was. If the condition was `False`, the
machine shows `Degraded` after the rollback. The same is true of
`ClockSynchronized`. To clear it, remove the condition from the
status with `kubectl edit machine <name> --subresource=status`.
