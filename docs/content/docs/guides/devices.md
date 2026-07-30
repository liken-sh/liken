---
title: Give a workload a device
weight: 70
---

# Give a workload a device

This guide gives a pod a piece of hardware: a GPU for a transcoder,
and a USB adapter that one pod holds alone. No step here needs a
privileged pod or a host path.

[Devices](/docs/reference/devices/) describes what liken publishes and
why.

## 1. See what a node offers

    kubectl get resourceslices
    kubectl get resourceslice <node>-liken.sh -o yaml

Each entry is one device you can claim:

    - name: pci-0000-00-02-0
      attributes:
        bus: {string: pci}
        class: {string: display}
        classCode: {string: "030000"}
        driver: {string: i915}
        modalias: {string: "pci:v00008086d000046D1..."}
        name: {string: Alder Lake-N [UHD Graphics]}
        product: {string: 46d1}
        renderNode: {bool: true}
        vendor: {string: "8086"}

A device that liken can share also carries
`allowMultipleAllocations: true`. A device carries it only when it
delivers a render node and every node it delivers comes from a
graphics subsystem. The integrated GPU above does not carry it,
because i915 also registers an i2c monitor-control node for each
display output, and those nodes are not graphics nodes. Such a device
allocates to one claim at a time.

If the hardware you expect is not in the list, no driver is bound to
it. Look at the hardware the machine reports that it cannot drive:

    kubectl get machine <name> -o jsonpath='{.status.hardware.unclaimed}'

## 2. Declare the driver

A device becomes available for a claim when a driver binds it. Add the
module to the machine's manifest:

    spec:
      modules:
        - i915

Apply the manifest, and the machine loads the module. Most modules
load without a reboot. The Machine's `SpecConverged` condition shows
when the change has an effect. The device is in the node's slice in a
few seconds.

The hardware report names these modules for you, as comments, when you
install the machine. See
[Install a cluster](/docs/guides/install/#first-run-the-hardware-report).

## 3. Say what your workload needs

A `DeviceClass` is a named set of conditions on hardware. Write one
for each kind of device that your deployments ask for.

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: gpu-render
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "liken.sh" &&
              has(device.attributes["liken.sh"].renderNode)

liken publishes an attribute only when it is true of the hardware, so
`has()` is the complete test. This class matches any GPU with a render
node, on any machine in the fleet. A selector that asks for a vendor
and a product ID names one model, and it stops matching on the next
machine you buy.

## 4. Claim the GPU for a deployment

The deployment writes a claim against the class, and the claim
allocates the GPU. The claim is also what places the pod: the
scheduler picks a machine whose `ResourceSlice` offers a matching
device.

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: transcoder-gpu
      namespace: media
    spec:
      devices:
        requests:
          - name: gpu
            exactly:
              deviceClassName: gpu-render

The pod names the claim, and each container that needs the device
names the pod's entry:

    spec:
      template:
        spec:
          resourceClaims:
            - name: gpu
              resourceClaimName: transcoder-gpu
          containers:
            - name: ffmpeg
              image: ...
              resources:
                claims:
                  - name: gpu

The container receives every node the device delivers. For the GPU
above, that is `/dev/dri/card0`, `/dev/dri/renderD128`, and the
`/dev/i2c-*` monitor-control nodes that i915 registers beside them.
Nothing else changes: no privilege, and no host mount. Your image supplies the
userspace driver, and the image's user must be able to open the node.
Check what the container received:

    kubectl exec deploy/transcoder -- ls -l /dev/dri

A device that carries `allowMultipleAllocations: true` serves more
than one claim: a second deployment writes its own `ResourceClaim`
against the same `DeviceClass`, and both deployments run. The
integrated GPU above does not carry it, so a second claim waits until
the first deployment releases the device.

Give a deployment that holds a claim like this the `Recreate`
strategy. A rolling update runs the old pod and the new pod at once,
both name the same claim, and Kubernetes gives a claim's device to
every pod that names the claim. `Recreate` stops the old pod first,
so one pod holds the device at a time.

## 5. Give one pod a device alone

Most devices are not shareable. A USB adapter has one control
endpoint, so liken publishes it as a device that allocates one time.
Select the device by its identity:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: zigbee-adapter
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "liken.sh" &&
              device.attributes["liken.sh"].vendor == "10c4" &&
              device.attributes["liken.sh"].product == "ea60"

Add `device.attributes["liken.sh"].serial == "..."` when a machine has
two adapters of the same model and you must select one of them. The
name of the device follows the port, not the unit, so a replacement
adapter in the same port has the same name.

Then give each pod its own claim:

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaimTemplate
    metadata:
      name: zigbee
      namespace: home
    spec:
      spec:
        devices:
          requests:
            - name: adapter
              exactly:
                deviceClassName: zigbee-adapter

    # in the pod template:
          resourceClaims:
            - name: adapter
              resourceClaimTemplateName: zigbee

A template makes one claim for each pod, and the device allocates to
one claim, so only one pod holds the adapter. A rolling update waits
until the old pod releases the adapter. This is correct for hardware
that one process must own.

Do not point two pods at one `ResourceClaim` for a device of this
kind. Both pods receive it. Kubernetes shares a claim with every pod
that names it, by design, and liken does not refuse the second pod. A
device node does not grant exclusive access by itself. The kernel is
what enforces exclusive access, through `O_EXCL` and the driver's own
open path. Use a template to give the device to one pod only.

## When a claim does not schedule

If a pod stays in `Pending` with a claim that is not allocated, then
usually no device matched the claim. Do these checks in this order:

1. `kubectl get resourceslices -o yaml` shows if the device is
   published. If the device is not there, no driver is bound to it:
   go back to step 2.
2. Compare your selector with the device's attributes. A selector that
   names an attribute the device does not have matches nothing.
3. If the device is published and it matches, another claim can
   already hold it. Find the holder:

       kubectl get resourceclaims -A -o wide

   A device that is not shareable allocates one time, and the second
   claim waits until the first claim releases it.
