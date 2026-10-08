# 79, Daemons restart inside their containers

Proposed, built, and drilled on liken-1 on 2026-10-08. The work
touches `media-operator`, `library-operator`, and `audio-operator`.
It follows display-operator's plan 29, which restarts the compositor
inside its container.

## The problem

The kubelet puts every exit of a container through its crash backoff,
whatever the exit code. A second exit within ten minutes waits 10 s
before the container starts again, a third waits 20 s, and the wait
doubles up to 5 minutes. A daemon that a component restarts on
purpose, by ending its container, pays that wait on every restart
after the first.

Plan 29 took display-operator's compositor restarts out of the
backoff. A survey of every component on 2026-10-08 then found three
more daemons that a normal event restarts through a container exit:

- `media-operator`'s idle client and `library-operator`'s media
  browser. Each exits with code 7 when a compositor restart takes its
  window. On liken-1, after a mode switch whose compositor was back in
  about 2 s, the browser stayed dark for 53 s. It had exited with code
  1, not 7, because its event loop ended with an error. The idle client
  exited a second time at its start, because the compositor was not up
  yet, so one compositor restart cost it two exits.
- `audio-operator`'s PipeWire and WirePlumber. A new channel layout
  took effect through a liveness probe that failed while the drop-in
  was newer than PipeWire's socket, so the kubelet restarted the
  PipeWire container, and WirePlumber exited after it. Plan 12 of
  `audio-operator` found that a receiver's ELD changes with its power,
  so a receiver turned on and off again within ten minutes reached the
  backoff.

The survey also found the `observatory-operator` device pods, whose
`socat` serves one connection and exits by design. That case is left
for later. The other daemons restart only on a crash, replace their
pod, or need a new container to receive a new device node.

## What changed

Each container's first process runs its daemon as a child, starts it
again in place for the one restart its component expects, and exits
with the daemon's status for any other exit. So the kubelet's backoff
still bounds a crash loop, and each container's restart count is the
count of crashes.

- **The screen clients** (`harness/respawn.rs` in each client). In a
  pod, where the window grace is set, the client runs itself as a
  child. When the child exits with code 7 and a new compositor bound
  the socket, the first process waits for the socket to accept a
  connection and starts the child again. A compositor is the device,
  the inode, and the change time of the socket it bound, because the
  kernel can give a new socket the inode of the one it replaced. A
  child that got no window from a compositor that still runs, and a
  compositor that does not answer within six minutes, end the
  container with code 7. The media browser now exits with code 7 when
  its event loop ends with an error, as the idle client does. The code
  is the same in both clients, as their watchdogs are, because
  `media-screen`, the crate they share, depends on the broker client
  and JSON alone, and the first process needs `libc` to pass the
  kubelet's `SIGTERM` to the child.
- **PipeWire** (`restarts.go` in `audio-operator`). The PipeWire
  container's first process watches the drop-in directory with
  inotify, and restarts PipeWire when the drop-in is newer than
  PipeWire's socket. The liveness probe that did this is gone.
- **WirePlumber** (the same file). The WirePlumber container's first
  process starts WirePlumber again when it exits because a new
  PipeWire bound the socket.

## The drills

The drills ran on liken-1 on 2026-10-08 with
`2026.10.08-003-dev-005-1087f1c7` of all three components.

Four mode switches of `boe-1080-display` on stick-1, 20 s apart, under
`lab-portable`'s media browser: the browser's restart count stayed at
0. Its log shows each lost window and each start, and the home page
opened again in 434 to 446 ms each time. A person watching the screen
saw the browser back after each switch.

Four mode switches of `gsm-7716-lg-hdr-wqhd` on liken-1, 20 s apart,
under `studio-lg`'s idle client: the restart count stayed at 0, and the
log shows four lost windows and four starts.

Three layout changes of `stick-1-pci-0000-00-0e-0-alc269vb-analog`
within 31 s: `LayoutApplied` read `Restarting` for about 3 s and
`Applied` about 5 s after each change. The PipeWire and WirePlumber
containers' restart counts stayed at 0, and their logs show three
restarts of each in place.

## What is not known

- What the idle client and the browser show while their compositor
  stays down for minutes. The first process waits up to six minutes,
  and the pod reads `Running` for that time. The `Display` reports the
  compositor down.
- Whether the WirePlumber probe for a lost Bluetooth bus should restart
  WirePlumber in place too. That restart still goes through the
  kubelet, and the bus is lost only when the Bluetooth pod is replaced.
