# An external layout engine

Plan 17 decides every placement in one function inside the operator:
the surfaces on a screen and their pods' labels and the `Layout` go
in, and a rectangle and a stacking position for each surface come
out. The executor in the compositor module commits whatever the
function returns and reasons about nothing.

That seam is where a different engine would go: one that puts the
camera with motion in the big region, one that follows a person's
attention from a remote, or one that an agent drives. The shape that
fits `liken` is the `Service`, `EndpointSlice`, and kube-proxy split.
The `Layout` is intent, a per-`Display` placement object is the
decision, and the operator's node agent executes it. A `Display`
would name its engine the way a pod names `schedulerName`, the stock
engine would act only on a `Display` that names none, and any other
engine would read `status.surfaces` and write the placement object
with plain RBAC and no socket.

Nothing here is built. It waits for a second engine to exist,
because an object with one writer and one reader in one binary is a
directory with one file in it. When one does, the work is: a
placement CRD whose spec is the decision function's return type, a
`spec.layoutEngine` field on `Display`, and the executor reading the
object instead of calling the function.

The cost that is known now is latency. A surface appears, the agent
reports it, the engine writes a placement, the agent commits it: two
trips through the API server, a few hundred milliseconds, where the
in-process function takes one pass of the loop. A film starting or
a camera arriving does not show that. Anything that tracks a hand on
a remote does, and belongs inside one client.
