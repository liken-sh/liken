# The spec becomes editable

Milestone 5 — Done

The spec becomes editable: a Machine edit in the cluster
actually converges, by reboot. The roles are now named for
their owners (`machineState` and `machineEphemeral` belong to
the machine; `clusterState` awaits `kind: Cluster`), and the
new `machineState` role holds the machine's manifests. The
operator detects drift between the cluster's spec and the
boot's boot record, validates against the machine's reality
(grow-only sizes, attached devices; CEL rules refuse shrinks at
admission), stages the manifest durably, and per
`spec.rebootPolicy` requests a reboot or reports one pending.
Init prefers the staged manifest, promotes it on success, and
falls back to the proven last-known-good on failure, so a bad
edit degrades the machine instead of taking it down. Partitions
are grow-only: sized roles grow into free space, remainder
roles follow a grown disk (relocating the backup GPT), and ext4
grows by ioctl, with no resize2fs.
1. [x] The `machine*` role vocabulary, and `machineState` first in
   canonical order so a boot can find it before reading any spec.
2. [x] A GPT reader (both copies, CRC-checked, identities preserved
   through edits) and grow-only partition resize, with the
   filesystem grown online via EXT4_IOC_RESIZE_FS.
3. [x] The manifest lifecycle on machineState: staged/proven/
   rejected, durable writes, the settle loop with last-known-good
   fallback, and the boot record in facts and status.
4. [x] The operator's convergence loop: drift detection, staging
   validation, the SpecConverged condition vocabulary,
   `spec.rebootPolicy`, and CEL no-shrink rules in the CRD.
5. [x] The reboot protocol: the operator's intent file, init's
   watcher, a graceful k3s stop, and `make run-lab` (a QEMU run
   that survives reboots) plus `grow-pods` for the disk-growth
   drill.
6. [x] Prove the full cycle in the lab: edit the spec via kubectl,
   watch the machine stage, reboot, grow, and converge; drill
   the rejections (CEL refuses a shrink at admission, the
   operator refuses an invalid spec with StagingRejected, and a
   staged spec that fails at boot falls back to proven and
   holds at RejectedLastBoot without a reboot loop). The
   disk-growth drill grew podEphemeral's partition and
   filesystem from 1.5 to 5.5 GiB in place.
7. [x] Editing back to a good state. The first CEL rules compared
   the spec against its previous value, which wedged: after
   declaring a size the machine couldn't satisfy, reverting the
   spec was also refused as a shrink, and the only exit was
   `kubectl replace --force`, which would be untenable once
   Flux owns the spec. The rules now compare the spec against
   `status.boot.storage` (the sizes the machine actually booted
   with), so a failed aspiration can always be edited back to
   reality, and only a real on-disk shrink is refused. When the
   spec returns to what the machine runs, the operator also
   withdraws any manifest still staged (the next boot would
   have applied it) and clears the standing rejection.
