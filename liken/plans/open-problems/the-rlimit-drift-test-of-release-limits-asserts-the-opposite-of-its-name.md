# The rlimit drift test of release limits asserts the opposite of its name

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`TestRlimitDriftIgnoresTheLimitsThatShipWithTheRelease`
(`machine/rlimitdrift_test.go:84`) says that `RlimitDrift` ignores the
limits in `OSRlimits`. Its second assertion checks that a spec that
repeats a release limit does produce a difference. Its first
assertion, `RlimitDrift(nil, nil)`, repeats the first assertion of
`TestRlimitDriftReportsNothingWhenNeitherSideDeclaresAnything` in the
same file. So no assertion tests what the name states.

## The evidence

`RlimitDrift` (`machine/drift.go:128`) compares the declared map with
the boot record and never reads `OSRlimits`. The test's second half
calls `RlimitDrift(map[string]string{"nofile": OSRlimits["nofile"]}, nil)`
and requires one difference. Its comment gives the reason: the boot
record holds only what the spec declared.

## What would confirm it

Read the two tests side by side. A change to `RlimitDrift` that
compared against `OSRlimits` would make the second assertion fail,
which is the opposite of what the name claims the function does.

## What a fix has to decide

Which behavior is the intended one. If the boot record holds only the
declared limits, the test needs a name that says a repeated release
limit is still drift, and the duplicate first assertion can go. If
release limits must not count as drift, `RlimitDrift` is wrong and the
test must say so.
