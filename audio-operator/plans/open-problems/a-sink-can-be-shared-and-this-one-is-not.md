# Every published sink is exclusive, though PipeWire can share one

Open problem. PipeWire mixes streams, so one sink can serve several
consumers at once. Every device this operator publishes is exclusive,
so the second pod to claim a monitor's speakers stays Pending while
the first pod holds them.

## Why exclusive was chosen

Milestone 59 chose it for two reasons, and both still apply. First,
the other two operators are exclusive, so one owner for one piece of
hardware gives a claim a clear meaning. Second, a claim on a shared
sink gives a workload no control over what else plays through it. A
pod that allocates the television's speakers cannot require exclusive
use. A video player that shares a sink with a notification sound is a
worse default than a video player that waits.

These reasons make exclusive the right default. They do not make a
shared sink wrong.

## What the API already offers

`allowMultipleAllocations` on a slice device marks it as allocatable
to more than one request. Its feature gate, `DRAConsumableCapacity`,
is beta and on by default in Kubernetes v1.36. This fact comes from
milestone 59's citation of `pkg/features/kube_features.go` on
`release-1.36`. Nobody read that file again for this note.

`liken` already sets the field. `publishDevices` in
`machine-operator/dra.go` writes
`device.AllowMultipleAllocations = &shared` for any delivery its
policy marks shareable. The integrated GPU is the case that caused
this: a real GPU is shared, while its display outputs stay exclusive.
So the mechanism exists in the layer below this operator, and it works
there.

`slices.go` in this operator does not set the field on any device it
publishes.

## What is not decided

The obvious extension is a second `DeviceClass` over the same devices,
one exclusive and one shared, so a consumer states which it wants.
Milestone 59 named this option and did not include it.

Nobody has decided these questions:

* Whether the two classes select the same devices, or whether a device
  opts in to being shareable at all. An HDMI output to a television
  and the analog jack to a desk speaker are possibly different cases.
* What a shared claim guarantees. `allowMultipleAllocations` says the
  device can be allocated more than once. It does not say the sink
  will still be audible over the other streams that play through it.
* Whether an exclusive claim and a shared claim on one device can
  exist at the same time, and, if they can, what the exclusive holder
  can expect.
* Whether anything needs this. No workload in this deployment has
  asked for a second stream on one sink, so nobody has measured the
  cost of the current behavior.

Nothing here is urgent. This note exists because "exclusive" is a
decision this operator made, and `slices.go` does not show that it
was a decision.
