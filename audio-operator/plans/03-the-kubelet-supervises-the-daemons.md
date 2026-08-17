# 03, The kubelet supervises the daemons

Built. Drilled on liken-1 on 2026-08-17, except the daemon-kill
fault drill, which is still owed.

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
as sibling containers, so this operator is the odd one out for no
reason its design states.

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
   against the socket, so readiness is a real client connecting, not
   a file existing.
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
dying and the operator noticing its connection is gone, which is the
same event. Under the supervisor the window was the pod's own
teardown. Neither is zero; both are honest within a second.

## What was considered and set aside

* **Keeping the supervisor.** It duplicates the kubelet with fewer
  features, and its one guarantee is a taint the operator already
  knows how to write.
* **A separate image per daemon.** The closure is one set of files
  that the daemons share almost entirely; two images would carry the
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
itself, and consumers limped along on the read-only grant because
playing needs no more. The fix is `config/51-access-rules.conf`: the
server's grant stands, because the CDI mount is the access control.
The release gate could not have caught this, because no cardless run
links a stream; a linking check is a candidate addition.

Still owed: the fault drill. Kill the PipeWire container, watch the
kubelet restart it alone, the operator republish, and the consumer
recover or evict on its toleration, with the all-devices taint
standing in the window where the graph is gone.
