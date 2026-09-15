# Nothing reports a crashed display

The display is a native sidecar in the playback pod, so the kubelet
restarts it alone and the pod stays `Running`. The `Play`'s phase
follows the pod's phase, so it stays `Running` too, and the bus carries
no mark of the crash: the command sidecar reports mpv's own properties
and holds nothing about the display's container.

A person sees a film that plays with no on-screen display, while
`kubectl get plays` and every bus topic say the run is healthy. Nothing
in the cluster names the fault. The only report is the restart count on
one container of one pod.

The operator already watches playback pods, so a container status with
a restart count or a terminated state is one read away from a condition
on the `Play`, or from the report the bus carries. The open question is
whether a crashed display is a `Play`-level fault at all, because the
film keeps playing without it.
