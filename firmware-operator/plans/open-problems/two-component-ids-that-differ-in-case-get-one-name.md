# Two component IDs that differ in case get one name

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

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

## The reproduction

A test calls `groupNames` for `com.hp.laptop.system.S70.firmware` and
`com.hp.laptop.System.S70.firmware`, and wants the second component to
have a name that the first does not take. It failed with:

```
the names of the second component are [hp-laptop-system-s70 com.hp.laptop.system.s70.firmware] and the first takes [hp-laptop-system-s70 com.hp.laptop.system.s70.firmware], want a name of the second that the first does not take
```

The test is not in the tree, because a failing test cannot land on
`main`. The test checks the names only. The error of
`createDiscovered` and the kept scan pod follow from them in the code.

## What a fix has to decide

- What name does the second of two components that differ only in
  case get, such as the whole ID with a hash of the exact ID as a
  suffix, as `discoveredName` does for a GUID set?
- Does a component that gets no name fail only its own group, so that
  the scan pod is deleted and the other groups are not written again
  on each pass?
- How do you learn that a component got no `Firmware`: an `Event`, or
  a condition on the `FirmwareCatalog`?
