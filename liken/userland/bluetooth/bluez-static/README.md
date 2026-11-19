# Patches to BlueZ

The bluetoothd image builds BlueZ from its release tarball with the
buildroot recipe in this directory. Before it configures the build,
buildroot applies each `.patch` file here to the source. Each patch
fixes a BlueZ bug that breaks a controller or a remote on `liken` and
that no BlueZ release fixes yet.

Each file is the output of `git format-patch` against the release
that `BLUEZ_STATIC_VERSION` in `bluez-static.mk` names. Its message
is written for the BlueZ mailing list, so you can send the same file
upstream. The image ships a copy of each patch under
`/usr/share/doc/liken/bluez-patches/`, because the GPL requires that
the source of a modified program is distributed with it.

buildroot applies each patch with `patch`, which fails when the
release already contains the change. When a new release contains the
fix, delete the patch file and its section below.

## `0001-device-re-enable-auto-connect-after-auth-retry.patch`

### The bug

In BlueZ 5.87, bluetoothd can turn off the kernel's automatic
reconnect of a bonded Bluetooth LE device and never turn it on again.
It logs nothing about this at the default log level. A remote that
sleeps between uses then does not reconnect on its own until
bluetoothd restarts.

When a link ends with an authentication failure,
`device_remove_connection()` in `src/device.c` turns auto connect off
and starts a timer of 1, 2, or 4 seconds. The timer's callback,
`device_auth_failure_retry()`, turns auto connect on again. When the
device connects again before the timer fires,
`device_add_connection()` removes the timer and resets the failure
count, but it does not turn auto connect on. The new connection
works. When the device next disconnects, for example because the
remote goes to sleep, the kernel has no reconnect entry for it.

Releases before 5.87 do not have this bug. Until BlueZ commit
7ca747652d ("device: Fix auth_retry timeout not being removed on
reconnect", 2026-06-08), `device_add_connection()` passed 0 to
`timeout_remove()`, so it did not remove the timer, and the timer
fired and turned auto connect on. 5.87 is the first release with that
commit.

### The fix

`device_add_connection()` calls `device_set_auto_connect(dev, TRUE)`
where it removes the timer. A connection that succeeds before the
timer fires shows that the keys work, so there is nothing left to
retry.

### Upstream

The patch is not sent yet. BlueZ `master` did not have a fix on
2026-10-17. [bluez/bluez#2594](https://github.com/bluez/bluez/issues/2594)
reports the same symptom on other radios, but it does not describe
this race. When you send the patch, link the mailing list thread here.

### Evidence

The race comes from reading the source. No trace has caught it yet.
On a home cluster that ran BlueZ 5.87, a remote that went to sleep
did not reconnect until the bluetoothd pod restarted, and no log line
appeared between the sleep and the restart. This race can cause that
failure. A kernel fault can cause the same failure, and this image
cannot patch the kernel, so `bluetooth-operator/bluetoothd/rescan/`
works around the kernel fault.

To tell the two apart, set `spec.btmon: true` on the radio's
`Adapter`. When this race causes the failure, the trace shows an
Encryption Change with status `0x06` (PIN or Key Missing), then
bluetoothd's `MGMT Command: Remove Device` for the remote, then a new
connection from the remote within 4 seconds.
