# Keep staged releases consistent during retargeting

Open bug, high priority. Changing the release target can overwrite an
inactive slot while its previous release remains staged. A later reboot
that `init` manages can arm that slot even though its files no longer
match the staged record.

## Failure path

Suppose release X is downloaded to slot B and has a staged `SystemRelease`
record. Before its proving reboot, the target changes to Y.

`convergeSystemRelease` in [release.go](../../machine-operator/release.go)
asks the fetcher for Y on the same inactive slot. While the fetch is not
`fetchVerified`, `decideSystemStaging` returns a condition without
withdrawing X's record. The old record can still start a trial.

`fetchRelease` in [fetch.go](../../machine-operator/fetch.go) verifies
and replaces artifacts one at a time. It writes `release.yaml` last.
An interrupted fetch can therefore leave Y's kernel beside X's boot
archive and release document. Each replacement file is verified, but the
files together are not one complete release.

`armProvingBoot` in [proving.go](../../init/proving.go) reads the staged
record and checks the running slot, proven slot, and fallback preference.
It does not verify the target slot's document digest or artifacts against
that record. The actuators' `canArmTrial` methods validate only the boot
mechanism: a boot entry in [efiactuator.go](../../init/efiactuator.go),
or a readable GRUB environment in [grubactuator.go](../../init/grubactuator.go).

`rebootMachine` in [reboot.go](../../init/reboot.go) runs this arming
path for every reboot that `init` manages, including a separate reboot
request. The reboot does not have to come from Y's version convergence.
An immediate power loss does not run this arming code.

## Consequences and evidence

A temporary fixture started with X staged on B, then interrupted Y's
fetch after replacing the kernel. The slot then held Y's kernel, X's
initramfs, X's release document, and X's staged record. The fixture used
small stand-in artifacts. No QEMU or hardware boot was performed. The
arming path was checked by reading the code.

A mixed slot may fail to boot. A completed Y download can also be tried
under X's record if the reboot occurs before staging catches up. That
can prevent promotion or attribute a failed trial to the wrong release.
The existing one-shot fallback and proving watchdog recover from some
failures. This review did not check that they recover from every failure
of a mixed image. Fallback is not limited to failures before `init`.

The network digest checks still run, and the finding does not show that
arbitrary attacker-provided bytes can run. The defect is that the trial
may not boot the complete release that its staged record names.

## Proposed safeguards

- Invalidate the previous stage durably before permitting writes for a
  different target. If the withdrawal fails, those writes must not start.
- Coordinate download cancellation, stage replacement, and reboot arming.
  A changed target must not start a second writer while the old one can
  still modify the slot.
- Before arming, verify the exact release document digest, version, and
  artifacts against the staged record. Writers must be stopped or excluded
  throughout verification and arming. If a write can happen after the
  check, there is still a race.
- In the durable lifecycle state, keep a complete verified slot separate
  from an incomplete download, including after process crashes and power
  loss.

This overlaps with [download cancellation](release-downloads-can-block-upgrades.md).
Cancellation does not invalidate a staged record, and it does not make
files that were already replaced consistent.

## Remedy scope

The fix is internal reliability work that the operator and `init` must
coordinate. It needs no new public API. The existing behavior of
retargets, manual approval, and fallback should stay the same.

Refusing all retargets, or changing what an approval authorizes, would
be separate product decisions. The conservative fix does not need
either one: never arm a slot whose verified contents disagree with its
staged record.

## Tests needed

Interrupt a retarget after each artifact and before the final stage write.
Attempt a separate reboot at each boundary. Verify withdrawal failures,
cancellation, crashes between writes, and mismatched document digests.
Exercise arming under both actuators and confirm that a concurrent writer
cannot modify a verified trial. Finally, verify ordinary upgrade,
promotion, and fallback in QEMU. The file fixture does not test them.
