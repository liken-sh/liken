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
