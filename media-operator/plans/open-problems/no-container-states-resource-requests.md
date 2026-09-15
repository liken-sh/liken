# No container states resource requests

No container this operator builds states a cpu or a memory request. The
player, the command sidecar, the display, the idle client, and the
standing remote reader all carry an empty `resources` block beside
their `resources.claims`. Every one of them lands in the `BestEffort`
QoS class, so the kubelet evicts them first under memory pressure and
the scheduler counts them as free.

This is not a regression. It has been true since the first pod, and the
display joined a pod that already worked this way.

What changed is the pod. It runs three containers, one of them a Vulkan
client with a graphics closure, on machines `liken` deploys with 1GB of
memory. That is the reason to decide the question once, for every pod
in this operator, rather than for the display alone.

The decision has two halves: whether a request is stated at all, and
what number a screen machine can carry. A request that is too large
parks a `Play` as `Pending` on the machine that holds its screen, which
has nowhere else to go.
