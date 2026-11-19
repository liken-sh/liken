# A restart intent that init cannot delete restarts k3s again

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When init cannot delete the restart intent file, it prints the error
and restarts k3s anyway. The file stays in the intent directory, so
the next scan of the directory finds it and restarts k3s again. The
comment above `scanIntents` says that init deletes the file before it
acts so that an interruption loses one request and never repeats it.
The code keeps that order only when the delete succeeds.

## The evidence

`scanIntents` (`init/reboot.go:116`) calls
`machine.ClearRestartIntent` (`init/reboot.go:134`). When it returns
an error, init prints "consuming the restart intent" and then sends
the intent on `restarts` all the same. `ClearRestartIntent`
(`machine/reboot.go:127`) ignores only `fs.ErrNotExist`, so every
other error from `os.Remove` leaves the file in place.

`watchForOperatorIntents` (`init/reboot.go:63`) calls `scanIntents`
on each wake of the directory watch, and once more each time the
component starts again after the watch fails. Each of those scans
reads the same restart intent and restarts k3s again.

The modules intent follows the same path (`init/reboot.go:155`). The
comment on `ModulesIntent` (`machine/reboot.go`) says that a repeated
modules intent is harmless, because init judges the staged store and
not the intent. A repeated restart has no such guard.

## What would confirm it

A test that runs `scanIntents` on an intent directory where the
restart intent cannot be removed, such as a directory that is made
read-only after the file is written, and then scans the directory a
second time. If both scans send a restart, the bug is confirmed.

## What a fix has to decide

- Whether a failed delete must stop the restart, so a request can be
  lost but never repeated, or whether init restarts once and records
  that it already acted on this intent, for example by its
  `RequestedAt`.
- How init reports a delete that keeps failing, so that a person sees
  it in status and not only on the console.
