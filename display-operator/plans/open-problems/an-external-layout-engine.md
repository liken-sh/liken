# An external layout engine

Plan 17 decides every placement in one function inside the operator:
the surfaces on a screen and their pods' labels and the `Layout` go
in, and a rectangle and a stacking position for each surface come
out. The executor in the compositor module commits whatever the
function returns and makes no placement decisions of its own.

That function is where a different engine would go: one that puts
the camera with motion in the big region, one that follows a
person's attention from a remote, or one that an agent drives. The
design that fits `liken` copies the split between `Service`,
`EndpointSlice`, and kube-proxy. The `Layout` is intent, a
per-`Display` placement object is the decision, and the operator's
node agent executes it. A `Display` would name its engine the way a
pod names `schedulerName`, the stock engine would act only on a `Display` that names none, and any other
engine would read `status.surfaces` and write the placement object
with plain RBAC and no socket.

Nothing here is built. It waits for a second engine to exist. With
one engine, the object would have one writer and one reader in one
binary, and it would add nothing that the function call does not
already do. When a second engine exists, the work is: a
placement CRD whose spec is the decision function's return type, a
`spec.layoutEngine` field on `Display`, and the executor reading the
object instead of calling the function.

The cost that is known now is latency. A surface appears, the agent
reports it, the engine writes a placement, the agent commits it: two
trips through the API server, a few hundred milliseconds, where the
in-process function takes one pass of the loop. That delay is not
visible when a film starts or a camera appears. It is visible in
anything that follows a person's input on a remote, so that kind of
layout belongs inside one client.
