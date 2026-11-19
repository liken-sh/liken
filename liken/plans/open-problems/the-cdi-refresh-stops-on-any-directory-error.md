# The CDI refresh stops on any directory error

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When machine-operator cannot read the CDI directory for any reason, it
skips the refresh of every prepared claim, records no failure, and
logs nothing. The comment at the check gives only one reason: the
directory does not exist until the first prepare of the boot.

## The evidence

`refreshCDISpecs` in `machine-operator/cdi.go` returns at once when
`os.ReadDir(cdiDir)` returns any error. A permission error or an I/O
error has the same result as a missing directory. The refresh is the
step that marks the device of a claim as unplugged, and the
`ResourceSlice` taints a marked device so that its pods are evicted
(`unplugged.go`). So when the read fails, an unplugged device of a
prepared claim gets no taint, and the pass records nothing to retry.

## What would confirm it

A test that makes `cdiDir` a directory that the test cannot read,
prepares a claim, removes its device from the fake sysfs, and runs a
pass. The test then checks whether the device is marked, and whether
the outcome records a failure.

## What a fix has to decide

Whether only `fs.ErrNotExist` returns without a failure, and every
other error is recorded with `out.fail`, so the pass retries.
