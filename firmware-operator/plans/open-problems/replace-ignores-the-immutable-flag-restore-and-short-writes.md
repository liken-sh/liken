# `replace` ignores the restore of the immutable flag and short writes

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`variableStore.replace` in `scan/apply.go` writes a whole EFI
variable. The capsule apply calls it through `repairUpdater` in
`scan/capsule.go`, to write the repaired partition node into fwupd's
boot entry. It has two faults that the append path of a Secure Boot
database update does not have:

- It sets the immutable flag again with `defer s.setFlags(path,
  flags)` and drops that call's error. The database write in the same
  file reports the same failure in its result, as ", and the immutable
  flag was not set again", and `TestAWrittenAppendWhoseFlagStaysClearIsWritten`
  in `scan/apply_test.go` tests that report. After `replace`, a
  variable whose flag stayed clear is not reported.
- It writes with `os.File.Write`, which writes the rest of the data
  again after a short write. `appendOnce` in `scan/apply_linux.go`
  uses one `write(2)` call for this reason: on efivarfs each call is
  one `SetVariable`, so a second call would send the tail of the
  variable as a write of its own.

## Evidence

- `replace` in `scan/apply.go`.
- The call `a.repairUpdater(vars, a.store.replace)` in `stageCapsule`
  in `scan/capsule.go`.
- `appendOnce` and its comment in `scan/apply_linux.go`.
- The restore report in `write` in `scan/apply.go`.

## What would confirm it

A test of `replace` with a store whose `setFlags` fails on the second
call. The test checks that `replace` or its caller reports the flag.
The short write needs a file that accepts part of a write, so a test
needs a store with a write function in place of `os.OpenFile`.

## What a fix has to decide

- Whether `replace` returns the restore error, or returns it apart
  from the write error so that `stageCapsule` reports it as the
  database write does, with the write still counted as done.
- Whether `replace` writes through the same single-call function as
  `appendOnce`, and whether `variableStore` gives both paths one
  write function.
