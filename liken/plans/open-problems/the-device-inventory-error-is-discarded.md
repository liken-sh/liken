# The device inventory's error is discarded

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`publishDeviceInventory` in `machine-operator/dra.go` returns its
error, and its comment says the error is returned so that the pass
retries. Its caller, `reconcile` in `machine-operator/reconcile.go`,
discards the return value with `_ =`.

## The evidence

The retry does not depend on the return value today. The pass's client
reports each failed request to the pass outcome through its observer
(`passOutcome.observe` in `outcome.go`), and the read and the write of
the `ResourceSlice` are requests of that client. So the return value
has no reader, and an error that is not from a request records no
failure.

## What would confirm it

A read of `reconcile` and of `publishDeviceInventory`. A test with an
API server that refuses the `ResourceSlice` write shows whether the
pass schedules a retry.

## What a fix has to decide

Whether `publishDeviceInventory` returns nothing and its comment names
the observer as the reason the pass retries, or whether `reconcile`
records the returned error with `out.fail`.
