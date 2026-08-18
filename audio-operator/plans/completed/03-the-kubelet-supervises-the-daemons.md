# 03, The kubelet supervises the daemons

Built, and drilled on liken-1 on 2026-08-17, the fault drill
included.

## The problem

The operator is PID 1. It writes PipeWire's node declarations, starts
PipeWire and WirePlumber as its own children, waits on them, and dies
with them. That supervision exists for one guarantee: a dead daemon
ends the pod, so the slice can never say an output plays while
nothing plays.

The cost is that the operator does a job the kubelet already does,
worse. A crashed daemon has no restart count of its own, no
per-container log stream, and no probe; the whole pod restarts to
repair one process. The bluetooth operator already runs its daemons
as sibling containers, so this operator is the only one that
supervises its own daemons, for no reason its design states.

## The design

Four containers, one image, ordered by Kubernetes rather than by the
operator.

1. **`declare`, an init container.** The operator binary in a second
   mode: it reads the claimed cards, writes the node declarations
   into an emptyDir that PipeWire mounts as its drop-in directory,
   and exits. This is the config-before-daemon ordering the
   supervisor used to provide, expressed as an init step.
2. **`pipewire`, a native sidecar** (an init container with
   `restartPolicy: Always`). Its startup probe execs `pw-dump`
   against the socket, so readiness means a real client connected,
   and not that a file exists.
3. **`wireplumber`, a second native sidecar**, started after
   PipeWire's probe passes, probed with `wpctl status`.
4. **The operator, the only regular container**, serving DRA. The
   supervision code is deleted.

Each daemon gets the kubelet's restart policy, its own logs, and its
own restart count. A crashed PipeWire restarts alone and re-reads
the declarations from the emptyDir; WirePlumber's connection dies
with it and its own restart reconnects.

## The coupling contract

The die-together guarantee survives as a taint instead of a process
tree. The display operator's `compositorDown` is the same shape for
the same reason: while nothing serves, the slice must say so.

* When the operator loses its connection to the graph, it publishes
  every device with the `audio.liken.sh/disconnected` NoExecute
  taint and exits nonzero. The kubelet restarts only the operator
  container, and its startup reconnects and publishes the truth.
* When PipeWire never answers within the startup timeout, the
  operator publishes the same all-tainted form before it exits, so a
  crashlooping daemon leaves a slice that says nothing plays.

The window where the slice is wrong is the moment between a daemon
dying and the operator reporting that its connection is gone, which
is the same event. Under the supervisor the window was the pod's own
teardown. Neither is zero; both are honest within a second.

## What was considered and set aside

* **Keeping the supervisor.** It duplicates the kubelet with fewer
  features, and its one guarantee is a taint the operator already
  writes.
* **A separate image per daemon.** The closure is one set of files
  that the daemons share almost entirely; two images would hold the
  same libraries twice and version-skew against each other.
* **WirePlumber or PipeWire as the pod's main container.** Sidecars
  start before regular containers, so a daemon in the main slot
  would start after the operator that needs it, or force the
  declare step back into the operator's startup.

## What the drill showed

The ordered startup ran on liken-1 on 2026-08-17, several times over:
declare completed, both sidecars passed their probes, and the
operator published the same slice the supervisor pod published. A
consumer restarted after a roll claims its output and plays.

The drill's real product was a defect the sibling design exposed and
the supervisor design had masked. WirePlumber's default access policy
reads `pipewire.sec.flatpak` before it trusts the access the server
granted, and the kernel cannot translate a peer's pid across PID
namespaces, so every client of the pod's socket looks sandboxed. The
policy lowered every client to read-only, WirePlumber's own client
included, and a read-only session manager cannot create a link, so
streams parked silently against healthy sinks. In the supervisor pod
WirePlumber shared PipeWire's PID namespace, so it never restricted
itself, and consumers still played on the read-only grant, because
playing needs no more. The fix is `config/51-access-rules.conf`: the
server's grant stands, because the CDI mount is the access control.
The release gate could not have caught this, because no cardless run
links a stream; a linking check is a candidate addition.

The fault drill killed PipeWire two ways. A single kill is repaired
in about two seconds: the kubelet restarts the PipeWire and
WirePlumber containers alone, and the recovery finishes before the
operator's detection, so no taint goes out. That is correct, because by the
time anything could taint, the outputs play again. The cost of a
fast restart is the documented one: a connected client goes silent
and has to restart, on the new pod exactly as on the old.

A sustained kill, held down by repeated kills until the kubelet's
crashloop backoff stretched the downtime past the detection window,
ran the whole contract: three failed graph reads in a row, one
write with every device tainted, a nonzero exit, and the kubelet
restarting the operator container alone. The pod was never
recreated (restart counts: pipewire 6, wireplumber 6, operator 1),
and `DeviceTaintManagerEviction` ended the consumer on its 30
second toleration. When the kills stopped, the backoff expired and
the pod converged unattended: ordered restart, honest slice, a
fresh consumer claiming and playing.

The drill also showed what the taint write provides. The kubelet
deletes a driver's `ResourceSlices` when the plugin deregisters,
which the operator's exit causes. A sustained outage's steady state
is then an absent slice, and a claim pends against nothing rather
than against a slice that is wrong. The all-tainted write covers the
gap between losing the graph and that deregistration, and it is
what causes the eviction.
