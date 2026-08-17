# A removed device has no bounded retry

Open problem. When a device leaves a ResourceSlice while a claim's
allocation still names it, the kubelet's `NodePrepareResources` retry
has no bound. So this operator taints a dark output and never deletes
it. The taint is a workaround, and it stands until Kubernetes bounds
the retry.

## What breaks if a device is deleted

A DRA driver publishes each output as a device in a ResourceSlice. The
scheduler allocates a device to a claim and records the allocation by
the device's name. At container start the kubelet calls the driver's
`NodePrepareResources` for that device.

If the operator deleted a device when its monitor went dark, the
allocation would still name it. `NodePrepareResources` would run
against a device that is in no slice, the driver would return an error,
and the kubelet would retry. Nothing bounds that retry. The pod would
retry for as long as the monitor stayed unplugged.

## The workaround

The operator keeps the device in the slice and taints it. The
`NoExecute` taint evicts the pod that holds the claim after the claim's
`tolerationSeconds`. The `NoSchedule` taint parks a new claim. A
monitor that comes back clears the taints. A device leaves the slice
only when its connector leaves the card, which does not happen on a
running machine.

## The upstream gap

Kubernetes bounds no retry for a device an allocation names but the
slice no longer lists.
[KEP-5322, "DRA: Handle permanent driver failures"](https://github.com/kubernetes/enhancements/issues/5322)
proposed to separate a permanent failure from a transient one and to
stop the retry on a permanent one. It was closed as not planned. Until
Kubernetes bounds the retry, the operator keeps a device and taints it,
which is the choice all three device operators make.
