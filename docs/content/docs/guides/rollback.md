---
title: Roll back
weight: 50
---

# Roll back

Two mechanisms return a machine to a version that works. A machine
falls back on its own when a new version fails its first boot. You
roll the fleet back deliberately by retargeting the Cluster.

## The automatic fallback

Every machine keeps two boot slots, A and B. It runs from one and
writes downloaded releases into the other. An upgrade reboots into
the new slot exactly once, as a trial:

* If the new kernel panics, the machine resets, and the firmware
  boots the proven slot. No software is involved.
* If the new version boots but does not rejoin the cluster within
  ten minutes, a watchdog reboots the machine, and it comes back up
  on the proven slot.

Either way, the machine ends up serving on the version it ran
before. Its phase shows Blocked, its conditions show
`RejectedLastBoot`, and
[`status.boot.systemRejection`](/docs/reference/machine/#statusboot)
records what happened. The rejection stands until you retarget
[`spec.version`](/docs/reference/cluster/#spec), so a machine never
retries a version that failed on it.

A bad release is never republished. The remedy is the next serial
number: publish a corrected release, catalog it, and point
`spec.version` at it.

## Deliberate rollback

To move the fleet back to an earlier release, point `spec.version`
at it:

    kubectl edit cluster

The version must still be in
[`spec.releases.catalog`](/docs/reference/cluster/#specreleasescatalog),
which is a reason to leave old entries in place. The same rollout machinery
applies: each machine downloads the older release into its inactive
slot, verifies it, and reboots on its granted turn, one machine at a
time. The cluster keeps serving throughout.
