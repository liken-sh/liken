# A claim's listener outlives the claim

Plan 17 opens one Wayland socket per claim, so the compositor knows
which claim a surface came from. libwayland adds a listening socket
with `wl_display_add_socket` and has no call that removes one. When
a claim ends, the module unlinks the path and stops accepting, and
the listener's file descriptor stays open in the compositor until
the compositor exits.

The compositor restarts on every mode change and every card flap,
so the count of descriptors between restarts is the count of claims
in that window, and on the lab machines that is under ten. A screen
that runs one mode for weeks with pods coming and going accumulates
one descriptor per pod. The process limit is 1024 by default, and
the module's log line on each open is what a reader would see first.

The fix that needs no upstream change is to open the socket in the
module with `socket`, `bind`, and `listen`, hand the descriptor to
`wl_display_add_socket_fd`, and keep the descriptor to `close` on
unprepare. libwayland's own event source on that descriptor then
reports an error once and is removed. That is not yet measured.
