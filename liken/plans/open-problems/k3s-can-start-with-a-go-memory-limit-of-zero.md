# k3s can start with a Go memory limit of zero

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

`k3sRuntimeEnv` (`init/supervisor.go`) writes the limit that
`GoMemoryLimitBytes` returns as `GOMEMLIMIT=<n>MiB`, and divides the
bytes by 2^20 with integer division. A limit under one mebibyte gives
`GOMEMLIMIT=0MiB`, and Go then collects garbage all the time. For a
limit from 1 byte to under 1 MiB, such as `512Ki`, `runtimeFacts`
(`init/facts.go:124`) reports `0Mi` in the `Machine`'s status, so the
status shows the same zero. A limit that resolves to 0 bytes, which a
percent gives when `machineMemoryBytes` returns 0 because sysinfo
failed, also gives `GOMEMLIMIT=0MiB`, but the status then reports no
limit, so the status and the environment of k3s disagree.

## The reproduction

A unit test called `k3sRuntimeEnv` and `runtimeFacts` with
`goMemoryLimit: 512Ki` and 1 GiB of memory, and with `goMemoryLimit:
25%` and a memory size of 0. It asserted that the environment of k3s
holds no `GOMEMLIMIT=0MiB`. It failed in both cases:

```
the environment of k3s is [GOMEMLIMIT=0MiB] and the status reports "0Mi", want no limit of 0 MiB
the environment of k3s is [GOMEMLIMIT=0MiB] and the status reports "", want no limit of 0 MiB
```

The first case shows that the status and the environment agree for a
small absolute limit. The second shows that they disagree when the
limit is 0 bytes. The test is not in the tree, because a failing test
cannot land on `main`.

## What a fix has to decide

When `goMemoryLimit` comes to less than 1 MiB on a machine, what
should k3s get? The status must report the same result.

- **Refuse the spec**, so a person sees the error and writes a larger
  limit or `off`.
- **Round up to 1 MiB.**
- **Turn the limit off**, as `off` does.

Decide also whether a percent limit is dropped when init cannot read
the machine's memory size.
