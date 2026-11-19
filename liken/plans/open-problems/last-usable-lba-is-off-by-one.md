# `LastUsableLBA` is off by one

Open bug. A unit test reproduced it on 2026-11-19. `liken`'s
partition tables leave one sector unused at the end of each disk, so
they disagree with every other partitioner. The fix is small, but a
fix alone makes every machine plan a one-sector growth of its system
disk, and storage then fails.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

The comment on the constants in `disks/gpt.go` (lines 71 to 75) says
the last usable sector is 34 sectors from the end, and `LastUsableLBA`
(`disks/gpt.go:94`) returns the one 35 from the end. The test
(`TestGPTLastUsableLBA`, `disks/gpt_test.go:21`) pins the wrong value.
This is conservative and loses one sector, and it makes `liken`'s
tables disagree with every other partitioner.

The backup table at the end of a disk is the 32 sectors of the entry
array and 1 sector of header, so the last usable sector is the total
minus 34. A unit test asserted that value for a disk of 4,194,304
sectors, and failed with:

```
LastUsableLBA(4194304) = 4194269, want 4194270
```

The test is not in the tree, because a failing test cannot land on
`main`.

## The fix

Fix the function and the test together.

The fix alone is not safe. Every existing remainder partition ends 35
sectors from the end, so after the fix `planGrowth` (`init/grow.go:42`)
plans a one-sector growth on every machine, because a role with no
declared size grows to the last usable sector (`init/grow.go:59`). On the system disk the
kernel refuses to read the new table while the boot slot is mounted,
and storage fails. The fix needs a tolerance of one sector in
`planGrowth`, and a test that a remainder at the old end stays
untouched.

The decision is when to ship the fix: now, together with the
one-sector tolerance and its test, or later, after growth on the
system disk works
([growing a role on the system disk fails the boot](growing-a-role-on-the-system-disk-fails-the-boot.md)).
