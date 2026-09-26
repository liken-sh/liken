---
name: rollback
description: "Return a machine or the fleet to a version that works: the automatic fallback after a failed first boot, and the deliberate rollback that points the Cluster at an earlier release. Use when an upgrade fails or misbehaves."
---

This skill is the guide at https://liken.sh/docs/guides/rollback/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Roll back

Two mechanisms return a machine to a version that works. If a new
version fails its first boot, the machine returns to the previous
version without help. You roll the fleet back deliberately when you
point the Cluster at an earlier version.

## The automatic fallback

Every machine keeps two boot slots, A and B. The machine runs from one
slot and writes downloaded releases into the other. An upgrade reboots
into the new slot one time only, as a trial:

* If the new kernel panics, the machine resets, and the firmware boots
  the proven slot. No software is involved.
* If the new version boots but does not rejoin the cluster in ten
  minutes, a watchdog reboots the machine. The machine then starts
  again on the proven slot.

In both cases, the machine serves on the version it ran before. Its
phase shows Blocked, its conditions show `RejectedLastBoot`, and
[`status.boot.systemRejection`](https://liken.sh/docs/reference/machine/#statusbootsystemrejection)
records what happened. The rejection stays until you point
[`spec.version`](https://liken.sh/docs/reference/cluster/#spec--version) at a
different version, so the machine does not boot the failed version
again.

A bad release is never published again. To correct a bad release,
publish a release with the next serial number, add it to the catalog,
and point `spec.version` at it.

## Deliberate rollback

To move the fleet back to an earlier release, point `spec.version` at
that release:

    kubectl edit cluster

The version must still be in
[`spec.releases.catalog`](https://liken.sh/docs/reference/cluster/#specreleasescatalog).
This is a reason to keep the old entries. The rollout is the same as
for an upgrade: each machine downloads the older release into its
inactive slot, verifies it, and reboots on its granted turn, one
machine at a time. The cluster continues to serve during the rollout.

### Before you roll back past a spec field

A release that is older than a field in the `Machine` spec cannot
read a manifest that uses that field. `spec.serio` is such a field:
releases before it reject a manifest that declares it. A machine
that declares `spec.serio` and boots an older release cannot use its
proven manifest. It boots the manifest from its install stick, if
the image carries one, or it powers off.

To roll back a machine that declares `spec.serio`, remove the field
first:

1. Remove `spec.serio` from the `Machine`:

       kubectl patch machine <name> --type=json \
         -p '[{"op":"remove","path":"/spec/serio"}]'

2. Wait until the `SpecConverged` condition reports that the removal
   is staged for the next boot, with the reason `RebootPending` or
   `AwaitingTurn`. A removed entry stays attached until that boot.
3. Point `spec.version` at the older release. The rollback's reboot
   applies the staged manifest, which the older release can read.

The older release does not know the `SerioAttached` condition, so it
keeps whatever value the condition had. If that value is `False`, the
machine reads as `Degraded` after the rollback. Remove the condition
from the status with `kubectl edit machine <name> --subresource=status`.
