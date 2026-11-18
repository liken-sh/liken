# `bondfetch` waits out every HCI error

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`readyAdapter` in `bondfetch/hci.go` reads each adapter index with
the `HCIGETDEVINFO` ioctl and skips an index whose read fails, whatever
the error. Only `ENODEV` means that no adapter has the index. An error
that every index returns, such as `EPERM` or `EACCES` from a change to
the pod's capabilities or user, makes `waitForAdapter` poll until
`adapterTimeout`, 30 seconds, and then fail with "no Bluetooth adapter
reported an address within 30s". That message names the wrong cause,
and the error that the kernel returned is not logged.

## Evidence

- `readyAdapter` tests `err != nil || adapter.Address.IsZero()` and
  continues on either.
- The doc of `hciSocket.deviceInfo` says an index with no adapter
  fails with `ENODEV`, so the code already knows which error means
  "no adapter".
- The fake reader in `bondfetch/hci_test.go` fails only with
  `ENODEV`. The tests cover an adapter that reports its address late,
  no address, no adapter, and empty indexes below the adapter. No case
  returns another error.

## What would confirm it

A test of `waitForAdapter` in a `synctest` bubble with a reader that
returns `EPERM` for every index. The test checks how long the wait
takes and which error it returns.

## What a fix has to decide

- Which errors mean "no adapter at this index" and which end the wait
  at once.
- Whether the final error names the last error from the kernel, so
  that `kubectl logs` of the init container shows it.
