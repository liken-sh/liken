# 04, The kubelet supervises the compositor

Planned on 2026-08-19. This plan moves Weston out of the operator's
process and into its own container, the shape the audio operator's
plan 03 built for PipeWire and WirePlumber. It is the prerequisite
for plan 05, which will restart the compositor to change a mode.

## The problem

Weston runs as a child process of the operator. One container holds
the DRA plugin, the slice writer, and the compositor, and a Weston
that exits ends all three. The coupling was deliberate, an operator
that outlives its compositor would advertise outputs it cannot
drive, but it prices every compositor restart as a driver restart.
A prepare call that needs the compositor restarted cannot trigger
the restart and answer the kubelet, because the answer dies with
the process.

The audio operator solved the same problem by giving each daemon
to the kubelet: PipeWire and WirePlumber run as init containers
with `restartPolicy: Always`, the kubelet restarts a crashed
daemon alone, and the operator container notices through the same
reads it always makes. The display operator predates that shape.

## The design

The pod becomes three containers from the one image:

* A `declare` init container runs first. It enumerates the card's
  connectors, the walk `main.go` does today, and writes
  `weston.ini` into a volume the compositor's container shares.
  The enumeration moves with the write, so the operator container
  no longer needs to run before the compositor.
* A `weston` init container with `restartPolicy: Always` runs the
  compositor. The entry is the operator binary in a compositor
  mode: it finds the card the claim delivered, execs `weston` with
  the flags and environment `startWeston` sets today, and Weston
  replaces the process, so Weston's exit is the container's exit
  and the kubelet's restart is the supervision. The exec keeps the
  card discovery in one binary instead of teaching the manifest a
  device path it cannot know.
* The `operator` container serves the DRA socket and writes the
  slice, and no longer starts, waits on, or ends the compositor.

What replaces the old coupling: the operator taints. Today a dead
Weston kills the operator, and the crash loop is what a person
sees. After this plan the operator watches the compositor's socket,
the same file `waitForSocket` polls at startup. While the socket is
gone, every output publishes the `disconnected` taint, the one
consumers already tolerate, because a dark compositor and a dark
cable mean the same thing to a claim: the output can serve nobody
right now. A prepare call fails plainly in that window. When the
socket returns, the operator re-reads the connectors and
republishes. That re-read also closes the restart case of the
stale-mode-list open problem: a compositor restart is exactly when
the kernel re-probes, and now it is exactly when the operator looks
again.

The socket directory stays the hostPath consumers mount, so a
delivery names the same path across every restart, which is the
same reasoning the Bluetooth operator's bus directory records.

## What this does not change

The compositor still dies with a mode change, and every client on
the card dies with it. This plan does not shrink that blast, it
moves the blast out of the DRA driver, so plan 05 can order one
without killing the process that must report it. Consumers of
display claims should run under controllers; the manual will say
so when plan 05 lands.

## The drill

On liken-1:

1. Kill the compositor container's process. The kubelet restarts
   that container alone, the operator's container keeps its uptime,
   and the slice taints and then clears without the pod restarting.
2. Time the dark window. The single-container measurement was 1.3
   seconds from death to outputs lit, and most of it was the
   kubelet's pod turnaround, which this plan removes from the path.
3. The movie demo across a compositor-only restart: the mpv pod
   still dies with its socket, which is the expected turbulence,
   and a controller-managed consumer must come back on its own.
