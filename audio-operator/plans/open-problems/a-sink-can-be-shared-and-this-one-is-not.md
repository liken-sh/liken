# A sink can be shared and this one is not

Open problem. PipeWire mixes streams, so one sink can carry several
consumers at once. Every device this operator publishes is exclusive,
so the second pod to claim a monitor's speakers parks Pending behind
the first.

## Why exclusive was chosen

Milestone 59 chose it for two reasons, and both still hold. The first
is that the other two operators are exclusive, and one owner for one
piece of hardware is the clearest thing a claim can mean. The second
is that a claim on a shared sink gives a workload no say over what
else plays through it: a pod that holds the television's speakers has
no way to say that it holds them alone, and a video player sharing a
sink with a notification sound is a worse default than a video player
that waits.

Neither reason says a shared sink is wrong. They say exclusive is the
right default, which is a smaller claim.

## What the API already offers

`allowMultipleAllocations` on a slice device marks it as allocatable
to more than one request. Its feature gate, `DRAConsumableCapacity`,
is beta and on by default in Kubernetes v1.36. Read from milestone
59's citation of `pkg/features/kube_features.go` on `release-1.36`,
not re-read here.

liken already sets the field. `publishDevices` in
`machine-operator/dra.go` writes
`device.AllowMultipleAllocations = &shared` for any delivery its
policy marks shareable, and the integrated GPU is the case that drove
it: a real GPU shares while its display outputs stay exclusive. So the
mechanism is in the layer below this operator, and it is proven there.

`slices.go` in this operator does not set the field on any device it
publishes.

## What is not decided

The obvious extension is a second DeviceClass over the same devices,
one exclusive and one shared, so a consumer states which it wants.
That was named in milestone 59 and left out of it.

What nobody has decided:

* Whether the two classes select the same devices, or whether a device
  opts in to being shareable at all. An HDMI output feeding a
  television and the analog jack feeding a desk speaker are not
  obviously the same case.
* What a shared claim promises. `allowMultipleAllocations` says the
  device can be allocated more than once. It does not say the sink
  will still be audible over whatever else holds it.
* Whether an exclusive claim and a shared claim on one device can
  coexist, and what the exclusive holder is entitled to expect if they
  can.
* Whether anything has actually wanted this. No workload in the house
  has asked for a second stream on one sink, so the cost of the
  current answer is unmeasured.

Nothing here is urgent. It is written down because "exclusive" is a
decision this operator made, and a reader of `slices.go` cannot tell a
decision from an omission.
