# No container states resource requests

No container this operator builds states a cpu or a memory request. The
player, the command sidecar, the display, the idle client, and the
long-running remote pod that reads controller input all have an empty
`resources` block beside their `resources.claims`. Every one of them is
in the `BestEffort` QoS class, so the kubelet evicts them first under
memory pressure and the scheduler counts them as free.

This has been true since the first pod, and the display joined a pod
that already worked this way.

What changed is the pod. It runs three containers, one of them a Vulkan
client with a graphics closure, on machines `liken` deploys with 1GB of
memory. So the question needs one decision for every pod in this
operator, not only for the display.

The decision has two parts: whether a container states a request at
all, and what number a screen machine can supply. A request that is
too large keeps a `Play` `Pending` on the machine that has its screen,
and the `Play` cannot run on any other machine.
