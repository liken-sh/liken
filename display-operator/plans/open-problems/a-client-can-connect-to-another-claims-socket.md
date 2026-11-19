# A client can connect to another claim's socket

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

Each claim on a screen gets its own Wayland socket, and the layout
module places every surface of a socket on the output that the socket
was opened for. The module and the operator identify a client's claim
by the socket it connected to. But `outputEdits` in `cdi.go` mounts the
whole socket directory, `/var/run/display.liken.sh`, read-write into
every client's container, and sets `WAYLAND_DISPLAY` to the claim's
own socket. The directory also holds the socket of every other claim
on the node and the compositor's own `wayland-0`. So a container that
connects to a socket other than the one in `WAYLAND_DISPLAY` draws as
that other claim, on that claim's screen, or on `wayland-0` outside
any claim. Isolation between claims depends on the client using the
variable it was given.

## Evidence

- `cdi.go`, `outputEdits`: one `Mounts` entry with
  `HostPath: socketDir` and `ContainerPath: socketDir`, options `rw`
  and `bind`.
- `main.go`: `socketName = "wayland-0"`, at
  `socketDir + "/" + socketName`.
- `claimsockets.go`, `claimSocketNames`: a claim's first socket is
  `wayland-<claim UID>`, and each later output of the same claim is
  `wayland-<claim UID>-<device>`, all in the same directory.
- Plan 17 keeps the layout module's control socket out of this
  directory, because "a consumer that could reach the control socket
  could place surfaces". No plan states the same choice for the
  Wayland sockets.

## The reproduction

The test built the CDI edits of one output for a claim whose socket is
`wayland-claim-a`, and asserted that no mount has the host path
`/var/run/display.liken.sh`, the directory that holds every socket. It
failed with:

```
the edits mount /var/run/display.liken.sh, which holds wayland-0 and the socket of every other claim, want only wayland-claim-a
```

The test is not in the tree, because a failing test cannot land on
`main`. The test shows the mount, not a client that uses it. On the
lab fleet, two claims on two screens of one node would show the rest:
from the first claim's pod, connect a client to the second claim's
socket and read `status.surfaces` of both `Display` resources.

## What a fix has to decide

Should each claim get a directory of its own, or a mount of its one
socket that still works after the compositor restarts and binds the
socket again?

CDI can mount one socket file per claim in place of the directory.
But `bind_listening_socket` in `liken-layout.c` unlinks the path and
binds a new socket each time the module listens, as after a compositor
restart, so a bind mount of the old file would name a removed socket.
The fix decides how a mount of one socket survives a compositor
restart, or whether each claim gets a directory of its own.
