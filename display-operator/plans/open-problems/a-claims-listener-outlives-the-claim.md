# A claim's listener outlives the claim

Plan 17 opens one Wayland socket per claim, so the compositor knows
which claim a surface came from. The module binds and listens on the
socket itself and hands the descriptor to
`wl_display_add_socket_fd`. libwayland has no call that removes a
listening socket, and it owns the descriptor from that point, so
closing the descriptor under its event source is not safe. When a
claim ends, the module unlinks the path and marks the name closed,
and the descriptor stays open in the compositor until the compositor
exits.

The count is one descriptor per `listen`, not one per claim name. A
`Deployment` with the `Recreate` strategy and a re-run `Job` both
keep their `ResourceClaim`, so the kubelet unprepares and prepares
the same claim, and each prepare binds a new descriptor at the same
path. The old one stays open with no path to reach it. The module's
log line names the descriptor on each open and on each close, which
is what a reader would see first.

The compositor restarts on every mode change and every card flap,
so the count of descriptors between restarts is the count of
prepares in that window, and on the lab machines that is under ten.
A screen that runs one mode for weeks with pods coming and going
accumulates one descriptor per prepare. The process limit is 1024 by
default.

The fix needs a call libwayland does not have: one that removes a
listening socket and returns or closes its descriptor. Upstream is
the place for it. Until then the module can only stop accepting by
unlinking the path.

An earlier version of the module named the socket to
`wl_display_add_socket`, which takes a `flock` on a lock file beside
the socket and holds it for the compositor's life. A `listen` on a
name the module had closed was then refused, and the pod stayed in
`ContainerCreating` until the compositor restarted. A descriptor the
module binds has no lock file, so that refusal is gone.
