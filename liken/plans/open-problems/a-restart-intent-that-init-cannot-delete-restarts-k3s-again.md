# A restart intent that init cannot delete restarts k3s again

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

When init cannot delete the restart intent file, it prints the error
and restarts k3s anyway. The file stays in the intent directory, so
the next scan of the directory finds it and restarts k3s again. The
comment above `scanIntents` says that init deletes the file before it
acts so that an interruption loses one request and never repeats it.
The code keeps that order only when the delete succeeds.

## The evidence

`scanIntents` (`init/reboot.go:82`) calls
`machine.ClearRestartIntent` (`init/reboot.go:100`). When it returns
an error, init prints "deleting the restart intent file" and then sends
the intent on `restarts` all the same. `ClearRestartIntent`
(`machine/reboot.go:127`) ignores only `fs.ErrNotExist`, so every
other error from `os.Remove` leaves the file in place.

`watchForOperatorIntents` (`init/reboot.go:40`) calls `scanIntents`
on each wake of the directory watch, and once more each time the
component starts again after the watch fails. Each of those scans
reads the same restart intent and restarts k3s again.

The modules intent follows the same path (`init/reboot.go:117`). The
comment on `ModulesIntent` (`machine/reboot.go`) says that a repeated
modules intent is harmless, because init judges the staged store and
not the intent. A repeated restart has no such guard.

A unit test wrote a restart intent, made the intent directory read-only
so that `os.Remove` fails, and ran `scanIntents` twice. It asserted
that the two scans send at most one restart, and failed with:

```
two scans of one restart intent that cannot be deleted sent 2 restarts, want at most 1
```

The test is not in the tree, because a failing test cannot land on
`main`. The intent directory is on tmpfs and init
runs as root, so a failed delete is not likely on a machine.

## What would confirm it

The unit test above confirms it.

## What a fix has to decide

- When init cannot delete a restart request, does it skip that
  restart, so a request can be lost but never repeated? Or does it
  restart k3s once and record that it already acted on this intent,
  for example by its `RequestedAt`?
- How does init report a delete that keeps failing, so that a person
  sees it in status and not only on the console?
