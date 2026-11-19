# `replace` ignores the restore of the immutable flag and short writes

Open bug, recorded on 2026-11-18. A unit test reproduced the first
fault on 2026-11-19. No unit test can show the second without a change
to `replace`.

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

## The reproduction

A test calls `replace` on a variable file in `t.TempDir()` with a
store whose `flags` reports the immutable flag and whose `setFlags`
fails on its second call, the restore. It wants an error from
`replace`. It failed with:

```
replace returned no error when the immutable flag was not set again, want the flag's failure
```

The test is not in the tree, because a failing test cannot land on
`main`.

For the short write, `replace` opens the file with `os.OpenFile` and
calls `os.File.Write` itself (`scan/apply.go:119` and `:123`), so a
test cannot give it a file that accepts part of a write. A test needs
`replace` to write through a function of `variableStore`, as the
append does with `write`.

## What a fix has to decide

- When the repaired boot entry is written and its immutable flag
  cannot be set again, does the apply count as `Written` with a note,
  as a database update does, or as `Uncertain`?
- Does `replace` write through the same single-call function as
  `appendOnce`, and does `variableStore` give both paths one write
  function?
