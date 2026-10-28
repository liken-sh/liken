# A named claim keeps an unplugged device

Open problem. A named `ResourceClaim` on a USB device stays allocated
to the device's old port after someone moves the cable to another port
or another machine, and the pods that use it fail on
`/dev/liken.sh/device-absent` until a person deletes the pods and the
claim and creates the claim again.
[Milestone 89](../completed/89-an-unplugged-device-evicts-its-pod.md)
repairs a claim that a pod owns by tainting the device, and does not
taint a named claim's device, because the taint cannot repair it.

## Why the taint loops on a named claim

On liken-1 on 2026-10-28, a one-replica `Deployment` held a named claim
on a Z-Wave dongle on stick-1. The taint evicted the pod after the
dongle moved to the next port. The evicted pod stayed in the claim's
`status.reservedFor` while it terminated, and the `ReplicaSet` created
a replacement at once. The scheduler reuses an allocated claim's
allocation for a new pod and does not read the allocated device's
taints again, so each replacement reserved the claim on the old port,
and the taint evicted it in turn. `kube-controller-manager` clears an
allocation only when `reservedFor` is empty, and it never was: the
controller posted 1,630 evictions in three minutes, until the
`Deployment` was scaled to zero. The allocation cleared within seconds
of that.

Kubernetes 1.37 has no step that stops new reservations of a claim
whose allocated device is gone. A claim that is being deleted takes no
new reservations, so a controller could delete a named claim and
create it again, but that makes `liken` rewrite an object that a
person or GitOps owns, and that option was rejected.

## Possible remedies

- An upstream change, such as a `NoExecute` taint on an allocated
  device that keeps the scheduler from reserving the claim for a new
  pod, or that allocates the claim again. Whether the device-taint
  enhancement (KEP 5055) names this case was not checked.
- The workload moves its claim into a `ResourceClaimTemplate`, which
  milestone 89 already repairs. `observatory-operator`'s device claims
  are named, and
  [a replugged USB device](../../../observatory-operator/plans/open-problems/a-replugged-usb-device.md)
  holds that case.
