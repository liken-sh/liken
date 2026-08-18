# The spec becomes editable

Milestone 05. Completed. An edit to a Machine in the cluster converges
on the machine through a reboot.

The roles are now named for their owners. `machineState` and
`machineEphemeral` belong to the machine. `clusterState` waits for the
`kind: Cluster` resource. The new `machineState` role holds the
manifests of the machine.

The operator finds the difference between the cluster spec and the boot
record of this boot. It validates that difference against the reality of
the machine: grow-only sizes and attached devices. CEL rules refuse a
shrink at admission. The operator stages the manifest durably. Then, as
`spec.rebootPolicy` says, it either requests a reboot or reports one as
pending. Init prefers the staged manifest. It promotes the manifest
after a good boot, and falls back to the proven last-known-good manifest
after a failure. Thus a bad edit degrades the machine, but does not take
it down completely.

Partitions are grow-only. Sized roles grow into free space. Remainder
roles follow a disk that grew, and this moves the backup GPT. ext4 grows
by an ioctl call, with no resize2fs.
1. [x] The `machine*` role vocabulary. `machineState` comes first in
   canonical order, so a boot can find it before it reads any spec.
2. [x] A GPT reader that reads both copies, checks their CRC, and keeps
   the identities through edits. Grow-only partition resize, with the
   filesystem grown online through EXT4_IOC_RESIZE_FS.
3. [x] The manifest lifecycle on machineState: staged, proven, or
   rejected. Durable writes. The settle loop, with last-known-good
   fallback. The boot record in facts and status.
4. [x] The convergence loop in the operator: drift detection, staging
   validation, the SpecConverged condition vocabulary,
   `spec.rebootPolicy`, and the CEL no-shrink rules in the CRD.
5. [x] The reboot protocol: the intent file from the operator, the
   watcher in init, a graceful k3s stop, `make run-lab` (a QEMU run that
   survives reboots), and `grow-pods` for the disk-growth drill.
6. [x] Prove the full cycle in the lab. Edit the spec through kubectl,
   and watch the machine stage, reboot, grow, and converge. Drill the
   rejections: CEL refuses a shrink at admission, the operator refuses
   an invalid spec with StagingRejected, and a staged spec that fails at
   boot falls back to proven and holds at RejectedLastBoot without a
   reboot loop. The disk-growth drill grew the podEphemeral partition
   and filesystem from 1.5 to 5.5 GiB in place.
7. [x] Edit back to a good state. The first CEL rules compared the spec
   against its previous value, and this stopped all recovery. After the
   spec declared a size that the machine could not satisfy, the rules
   also refused a revert of the spec, because they read the revert as a
   shrink. The only way out was `kubectl replace --force`, which will
   not work when Flux owns the spec. The rules now compare the spec
   against `status.boot.storage`, the sizes that the machine booted
   with. Thus an edit can always bring a failed spec back to
   reality, and the rules refuse only a real on-disk shrink. When the
   spec returns to what the machine runs, the operator also withdraws a
   manifest that is still staged, because the next boot would apply it,
   and it clears the standing rejection.
