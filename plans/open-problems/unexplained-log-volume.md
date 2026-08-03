# containerd and the kubelet write lines that no log level explains

Open problem. `spec.runtime.containerd.logLevel` and
`spec.runtime.k3s.debug` set the levels, so what stays open is what
the levels do not reach.

## What the levels do not reach

A five-machine fleet's machine streams wrote 295,000 lines a day, and
containerd's pod lifecycle at info was 171,000 of them. containerd
logs `container event discarded` when nothing consumes its event
stream, and that line alone is a third of containerd's volume, so it
may mark a missing subscriber rather than verbosity.

A reboot is followed by about a day of elevated logging, while the
kubelet's garbage collector removes the sandboxes the reboot orphaned.

A floor also stays under any level. The CNI plugin writes about three
lines per pod on its own stderr, which no log level reaches. The lab
measured it at 30 lines across a ten-pod churn that wrote 211 before
the level was set.
