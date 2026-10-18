# Patches to BlueZ

The bluetoothd image builds BlueZ from its release tarball, and
`bluetoothd/Dockerfile` applies each file in `bluez/` to that source
before it configures the build. Each patch fixes a BlueZ bug that
stops a `liken` controller from working, and that no BlueZ release
fixes yet. A patch here is a stopgap: it stays only until a release
that `BLUEZ_VERSION` names carries the same fix.

Each file is the output of `git format-patch` against the release
that `BLUEZ_VERSION` names. Its message is written for the BlueZ
mailing list, so the same file is what goes upstream. The image ships
a copy of each patch under `/usr/share/doc/liken/bluez-patches/`,
because the GPL requires that the source of a modified program travel
with it.

The Dockerfile applies each patch with `patch --forward`. When a new
release already carries a fix, the patch no longer applies forward,
and the build stops. Then delete the file and its entry below.

## `0001-device-re-enable-auto-connect-after-auth-retry.patch`

**The bug.** BlueZ 5.87 can turn off the kernel's reconnect of a
bonded Bluetooth LE device for good, with no log line at the default
log level. A remote that sleeps between uses then never reconnects on
its own, and only a restart of bluetoothd recovers it.

When a link ends with an authentication failure,
`device_remove_connection()` in `src/device.c` turns auto connect off
and starts a timer of 1, 2, or 4 seconds. The timer's callback,
`device_auth_failure_retry()`, turns auto connect on again. When the
device connects again before the timer fires,
`device_add_connection()` removes the timer and resets the failure
count, but does not turn auto connect on again. The new connection
works. After the next disconnect, such as the remote going to sleep,
the kernel holds no reconnect entry for the device.

Before BlueZ commit 7ca747652d ("device: Fix auth_retry timeout not
being removed on reconnect", 2026-06-08), `device_add_connection()`
passed 0 to `timeout_remove()`, so the timer fired anyway and turned
auto connect on. 5.87 is the first release with that commit, so 5.82
did not have this bug.

**The fix.** `device_add_connection()` calls
`device_set_auto_connect(dev, TRUE)` where it removes the timer. The
connection that cancels the retry proves the keys work, so the retry
has nothing left to wait for.

**Upstream.** Not sent yet. Not fixed on BlueZ `master` as of
2026-10-17, and not reported in
[bluez/bluez#2594](https://github.com/bluez/bluez/issues/2594), which
describes the same symptom on other radios. When the patch is sent,
link the thread here.

**Evidence.** The race comes from reading the source, and no trace
has caught it yet. A home cluster on BlueZ 5.87 lost a sleeping
remote until a restart of the pod, with no log line between the
remote's sleep and the restart. This race fits that stall, and so
does a kernel fault with the same symptom, which this image cannot
patch. `bluetoothd/rescan/` works around the kernel fault. With
`spec.btmon` on, a stall from this race shows an Encryption Change
with status `0x06` (PIN or Key Missing), then bluetoothd's
`MGMT Command: Remove Device` for the remote, then a connection to it
within 4 seconds.
