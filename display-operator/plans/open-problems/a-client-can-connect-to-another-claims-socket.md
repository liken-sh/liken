# A client can connect to another claim's socket

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

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
- `claimsockets.go`, `claimSocketNames`: each claim's sockets are
  `wayland-<claim UID>` in the same directory.
- Plan 17 keeps the layout module's control socket out of this
  directory, because "a consumer that could reach the control socket
  could place surfaces". No plan states the same choice for the
  Wayland sockets.

## What would confirm it

On the lab fleet, two claims on two screens of one node. From the
first claim's pod, connect a client to the second claim's socket and
read `status.surfaces` of both `Display` resources.

## What a fix has to decide

CDI can mount one socket file per claim in place of the directory.
But `bind_listening_socket` in `liken-layout.c` unlinks the path and
binds a new socket each time the module listens, as after a compositor
restart, so a bind mount of the old file would name a removed socket.
The fix decides how a mount of one socket survives a compositor
restart, or whether each claim gets a directory of its own.
