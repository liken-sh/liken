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

## What sits at ERROR

The same fleet's k3s stream wrote 13,149 ERROR lines a day, its
containerd stream 1,751, and its kernel stream 22. Three classes are
counted out of the k3s total:

* 2,875 are the [apiserver aborts](apiserver-aborts.md).
* 4,176 are the kubelet reading a pod's cgroup after the pod ended:
  `failed to read memory cgroup config for the pod` and `failed to
  read memory cgroup limits for the pod`, one for `memory.max` and one
  for `cpu.max`. Every sample named a CronJob pod.
* 471 are the kubelet's status manager reading a pod the node no
  longer holds, which the node authorizer refuses with `no
  relationship found between node '<name>' and this object`.

The remaining 5,600 or so are unattributed. Each class is a race that
resolved on its own, and each one asks a person to look at ERROR, so
the volume is the cost rather than the fault.
