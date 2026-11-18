# k3s can start with a Go memory limit of zero

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`k3sRuntimeEnv` (`init/supervisor.go`) writes the limit that
`GoMemoryLimitBytes` returns as `GOMEMLIMIT=<n>MiB`, and divides the
bytes by 2^20 with integer division. A limit under one mebibyte gives
`GOMEMLIMIT=0MiB`. The reader of the same spec that fills the
`Machine`'s status can report no limit for that value, so the status
and the environment of k3s would disagree.

## What would confirm it

A unit test that calls `k3sRuntimeEnv` with a spec and a memory size
whose computed limit is under 2^20 bytes, and reads the variable, and a
second test that reads what the status reports for the same input.

## What a fix has to decide

Whether a limit under one mebibyte is refused, rounds up, or turns the
limit off, and that the status reports the same result.
