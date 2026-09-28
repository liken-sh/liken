# The connection to the media bus polls for the socket

Open problem, low priority. When the Bluetooth media bus is not there,
the operator asks for it again once a second. It does this at start
(`waitForBus` in [bluez.go](../../bluez.go)), and after the bus closes
while the operator runs (`resubscribe`). The organization's rule is to
wait on an event, not on a timer that reads state again.

## What happens

The Bluetooth operator's pod serves the bus. Its CDI delivery mounts
the socket's directory, `/var/run/bluetooth.liken.sh/dbus`, into this
pod. When that pod restarts, its `dbus-daemon` exits, and this
operator's connection closes. The operator then calls
`dbus.SystemBus` once a second until the new `dbus-daemon` answers,
with no limit but the operator's own shutdown. At start the same wait
ends the process after 30 seconds.

The poll runs only while the bus is gone: a few seconds for each
restart of the Bluetooth operator's pod. It sends nothing while the
bus answers.

## Why it matters

The cost is small, but a poll is the pattern that the organization
replaces with events. Each retry that fails also costs a connect on a
socket path that does not answer yet.

## What a fix could look like

The mounted directory is visible to this container. An inotify watch
on that directory, opened before a first connect attempt, reports the
new `system_bus_socket` when the new `dbus-daemon` creates it. The
operator would connect on that event, and once when the watch opens,
the same as the other inotify sources the organization uses.

## What is not known

* Whether a hostPath directory that the CDI delivery mounts reports
  inotify events for a socket that another pod's container creates.
  The two containers see the same host directory, so it should, but
  nobody has drilled it.
* Whether the socket file is present before `dbus-daemon` accepts on
  it. If it is, an event can arrive before the bus answers, and the
  connect still needs one retry.
