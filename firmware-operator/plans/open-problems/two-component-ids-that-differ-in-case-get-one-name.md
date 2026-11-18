# Two component IDs that differ in case get one name

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`createDiscovered` in `discovered.go` tries the names that
`groupNames` in `grouping.go` returns for a catalog component, in
order: `shortComponentName`, and then `componentName`, the whole ID.
Both names pass through `slug`, which lowercases the text and turns
each run of other characters into one `-`. So two component IDs that
differ only in case, or only in the characters that `slug` drops, give
the same two names. When the first component's object has both names,
`createDiscovered` returns the error "the names ... belong to other
Firmware objects" for the second component, and the second component
gets no `Firmware`.

The failure repeats. `applyScan` returns the error, and the caller in
`scans.go` keeps the scan pod so that the next pass reads its result
again. Each pass then calls `createDiscovered` again, fails again, and
keeps the pod. The other groups of the same scan are reconciled again
on each pass, and the scan pod is never deleted.

## Evidence

- `groupNames`, `shortComponentName`, `componentName`, and `slug` in
  `grouping.go`.
- The error at the end of `createDiscovered` in `discovered.go`.
- The comment "The pod is kept, so the next pass reads its result
  again" in the loop after `applyScan` in `scans.go`.
- Plan 03 measured the LVFS stable catalog on 2026-10-20: 6 pairs of
  its 2,748 component IDs differed in case alone, such as
  `com.hp.laptop.system.S70.firmware` and
  `com.hp.laptop.System.S70.firmware`. The plan states that the whole
  ID in lowercase gives each pair one name, and records no remedy.

## What would confirm it

A test of the discovery world with one machine whose scan reports two
devices, one for each of two component IDs that differ only in case.
The test checks that each component gets a `Firmware`, and that the
scan pod is deleted after the pass.

## What a fix has to decide

- Which third name the second component takes, such as the whole ID
  with a hash of the exact ID as a suffix, as `discoveredName` does for
  a GUID set.
- Whether a component that gets no name fails only its own group, so
  that the scan pod is deleted and the other groups are not written
  again on each pass.
- How a person learns that a component has no `Firmware`: an `Event`
  or a condition on the `FirmwareCatalog`.
