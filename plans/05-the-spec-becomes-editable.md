# The spec becomes editable

Milestone 5 — Done

The spec becomes editable. A Machine edit in the cluster now
converges, through a reboot. The roles are now named for their
owners. `machineState` and `machineEphemeral` belong to the
machine. `clusterState` awaits the `kind: Cluster` resource. The
new `machineState` role holds the machine's manifests. The
operator detects drift between the cluster's spec and the
boot's own boot record. It validates the drift against the
machine's reality: grow-only sizes and attached devices. CEL
rules refuse shrinks at admission. The operator stages the
manifest durably. Then, following `spec.rebootPolicy`, it either
requests a reboot or reports one as pending. Init prefers the
staged manifest. It promotes the manifest on success, and falls
back to the proven last-known-good manifest on failure. Because
of this, a bad edit degrades the machine instead of taking it
down completely. Partitions are grow-only. Sized roles grow into
free space. Remainder roles follow a grown disk, and this
relocates the backup GPT. ext4 grows by ioctl call, with no
resize2fs.
1. [x] The `machine*` role vocabulary. `machineState` comes first in
   canonical order, so a boot can find it before it reads any
   spec.
2. [x] A GPT reader that reads both copies, checks their CRC, and
   preserves identities through edits. Grow-only partition
   resize, with the filesystem grown online through
   EXT4_IOC_RESIZE_FS.
3. [x] The manifest lifecycle on machineState: staged, proven, or
   rejected. Durable writes. The settle loop, with
   last-known-good fallback. The boot record in facts and
   status.
4. [x] The operator's convergence loop: drift detection, staging
   validation, the SpecConverged condition vocabulary,
   `spec.rebootPolicy`, and CEL no-shrink rules in the CRD.
5. [x] The reboot protocol: the operator's intent file, init's
   watcher, a graceful k3s stop, and `make run-lab` (a QEMU run
   that survives reboots), and `grow-pods` for the disk-growth
   drill.
6. [x] Prove the full cycle in the lab. Edit the spec through
   kubectl, and watch the machine stage, reboot, grow, and
   converge. Drill the rejections: CEL refuses a shrink at
   admission, the operator refuses an invalid spec with
   StagingRejected, and a staged spec that fails at boot falls
   back to proven and holds at RejectedLastBoot without a reboot
   loop. The disk-growth drill grew podEphemeral's partition and
   filesystem from 1.5 to 5.5 GiB in place.
7. [x] Editing back to a good state. The first CEL rules compared
   the spec against its previous value, and this got stuck.
   After the spec declared a size the machine could not satisfy,
   the rules also refused a revert of the spec, because they
   read the revert as a shrink. The only way out was `kubectl
   replace --force`, which would not work once Flux owns the
   spec. The rules now compare the spec against
   `status.boot.storage`, the sizes the machine actually booted
   with. Because of this, an edit can always bring a failed
   aspiration back to reality, and the rules refuse only a real
   on-disk shrink. When the spec returns to what the machine
   runs, the operator also withdraws any manifest still staged,
   since the next boot would have applied it, and clears the
   standing rejection.
