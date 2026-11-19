# A storage refusal before `machineState` mounts leaves no record

Open bug, recorded on 2026-11-18. It is not reproduced. The claim was
checked against the code on 2026-11-19, and the comment at the top of
`init/failstop.go` states the same gap. No unit test can show it,
because `reconcileStorage` needs real block devices. A boot of a `lab`
guest, described below, would show it.

When a boot from disk stops because a declared disk never attaches, or
because of another storage refusal before `machineState` is mounted,
the machine powers off and writes no fail-stop record. The next boot
refuses the same way, so `status.lastFailStop` never shows the reason,
even when the disk that holds `machineState` is healthy. The comment
at the top of `init/failstop.go` states this gap, and no plan or open
problem records it.

## The evidence

`recordFailStop` (`init/failstop.go:42`) writes only while
`machineStateWritable` is true, and `mountRole` sets that flag
(`init/storage.go:523`). `reconcileStorage`
(`init/storage.go:248`) waits for the declared disks
(`init/storage.go:261`), then plans every claim and growth, and mounts
the roles last (`init/storage.go:326`). A refusal in any step before
the mounts reaches `failBoot` (`init/main.go:533`) with the flag false.

## What would confirm it

A `lab` guest whose `Machine` declares a second disk that the guest
does not have. Boot it twice from disk, and read
`status.lastFailStop`.

## What a fix has to decide

Where the record goes when the refusal comes before the mounts. One
way is for `failBoot` to find the `machineState` partition with
`findMachineStatePartition` (`init/manifests.go:65`), which the boot
already uses to read the manifests, and to mount it only to write the
record. That mount must not change a disk
that the refusal says is in a wrong state.
