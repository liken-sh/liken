# The wpa_supplicant control channel can panic on close

Open bug. Init can panic during a shutdown when a radio settles after
the shutdown began, because the reader of the `wpa_supplicant` control
socket can send on a channel that `close` already closed. A panic in
init during a shutdown stops the machine's orderly reboot.

The survey of 2026-09-11 in
[milestone 66](../completed/66-durability-and-safety.md) found this,
and [milestone 94](../completed/94-durability-and-safety-fixes-that-are-not-built.md)
held it until 2026-11-17.

## The evidence

`wpaControl.close` (`init/wpactrl.go:355`) closes the event channel
under the lock, and the read goroutine (`read`, `init/wpactrl.go:331`)
sends on that channel without the lock. Closing the socket first does
not stop a goroutine that is already past `Read`. A send on a closed
channel panics, also inside a `select` with a `default`.

`close` has one caller, for a radio that settles after the shutdown
began, so this can happen at most once, during a shutdown.

## The fix

A done channel the reader checks, or a close that waits for the
reader.
