# `LastUsableLBA` is off by one

Open bug. `liken`'s partition tables leave one sector unused at the
end of each disk, so they disagree with every other partitioner. The
fix is small, but a fix alone makes every machine plan a one-sector
growth of its system disk, and storage then fails.

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

## The fix

Fix the function and the test together.

The fix alone is not safe. Every existing remainder partition ends 35
sectors from the end, so after the fix `planGrowth` (`init/grow.go:54`)
plans a one-sector growth on every machine. On the system disk the
kernel refuses to read the new table while the boot slot is mounted,
and storage fails. The fix needs a tolerance of one sector in
`planGrowth`, and a test that a remainder at the old end stays
untouched.
