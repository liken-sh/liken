# Keep staged releases consistent during retargeting

Open bug. Review priority: high. Changing the release target can overwrite
an inactive slot while its previous release remains staged. A subsequent
init-managed reboot can arm that slot even though its files no longer
match the staged record.

## Failure path

Suppose release X is downloaded to slot B and has a staged `SystemRelease`
record. Before its proving reboot, the target changes to Y.

`convergeSystemRelease` in [release.go](../../machine-operator/release.go)
asks the fetcher for Y on the same inactive slot. While the fetch is not
`fetchVerified`, `decideSystemStaging` returns a condition without
withdrawing X's record. The old record remains eligible for a trial.

`fetchRelease` in [fetch.go](../../machine-operator/fetch.go) verifies
and replaces artifacts individually. It writes `release.yaml` last.
An interrupted fetch can therefore leave Y's kernel beside X's boot
archive and release document. Individual replacement files are verified,
but the set of files is not one complete release.

`armProvingBoot` in [proving.go](../../init/proving.go) reads the staged
record and checks the running slot, proven slot, and fallback preference.
It does not verify the target slot's document digest or artifacts against
that record. The actuators' `canArmTrial` methods validate only the boot
mechanism: a boot entry in [efiactuator.go](../../init/efiactuator.go),
or a readable GRUB environment in [grubactuator.go](../../init/grubactuator.go).

`rebootMachine` in [reboot.go](../../init/reboot.go) invokes this arming
path for init-managed reboots, including a separate reboot request.
It need not be a reboot requested by Y's version convergence. An immediate
power loss is different: it does not itself execute this arming code.

## Consequences and evidence

A temporary fixture started with X staged on B, then interrupted Y's
fetch after replacing the kernel. It retained Y's kernel, X's initramfs,
X's release document, and X's staged record. The fixture used small
stand-in artifacts; no QEMU or hardware boot was performed. The arming
path was verified by reading the code.

A mixed slot may fail to boot. A completed Y download can also be tried
under X's record if the reboot occurs before staging catches up. That
can prevent promotion or attribute a failed trial to the wrong release.
The existing one-shot fallback and proving watchdog provide recovery in
some failure cases, but this review did not establish recovery for every
mixed-image failure. Fallback is not limited to failures before `init`.

This does not bypass the network digest checks or demonstrate execution
of arbitrary attacker-provided bytes. It breaks the guarantee that the
trial boots the complete release named by its staged record.

## Proposed safeguards

- Invalidate the previous stage durably before permitting writes for a
  different target. Failure to withdraw it must prevent those writes.
- Coordinate download cancellation, stage replacement, and reboot arming.
  A changed target must not start a second writer while the old one can
  still modify the slot.
- Before arming, verify the exact release document digest, version, and
  artifacts against the staged record. Writers must be stopped or excluded
  throughout verification and arming; checking first and allowing another
  write afterward leaves a race.
- Keep a complete verified slot separate from an incomplete download in
  the durable lifecycle, including after process crashes and power loss.

This intersects [download cancellation](release-downloads-can-block-upgrades.md).
Cancellation alone does not invalidate a staged record or make already
replaced files consistent.

## Remedy scope

**Coordinated internal reliability work.** The existing retarget, manual
approval, and fallback contracts should remain. This needs cooperation
between the operator and `init`, not a new public API.

Refusing all retargets or changing what an approval authorizes would be
separate product decisions. They are not necessary assumptions for the
conservative fix: never arm a slot whose verified contents disagree with
its staged record.

## Tests needed

Interrupt a retarget after each artifact and before the final stage write.
Attempt a separate reboot at each boundary. Verify withdrawal failures,
cancellation, crashes between writes, and mismatched document digests.
Exercise arming under both actuators and confirm that a concurrent writer
cannot modify a verified trial. Finally, verify ordinary upgrade,
promotion, and fallback in QEMU; the file fixture does not prove them.
