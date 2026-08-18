---
title: Give a workload a device
weight: 70
---

# Give a workload a device

This guide gives a pod a piece of hardware: a GPU for a transcoder,
and a USB adapter that one pod holds alone. No step here needs a
privileged pod or a host path.

[Devices](/docs/reference/devices/) describes what `liken` publishes and
why. The [hardware operators](/docs/concepts/hardware-operators/)
publish devices the operating system does not: paired Bluetooth
controllers, monitor outputs, and audio outputs.

## 1. See what a node offers

    kubectl get resourceslices
    kubectl get resourceslice <node>-liken.sh -o yaml

Each entry is one device you can claim:

    - name: pci-0000-00-02-0
      allowMultipleAllocations: true
      attributes:
        address: {string: "0000:00:02.0"}
        bus: {string: pci}
        class: {string: display}
        classCode: {string: "030000"}
        driver: {string: i915}
        modalias: {string: "pci:v00008086d000046D2..."}
        name: {string: Alder Lake-N [UHD Graphics]}
        product: {string: 46d2}
        renderNode: {bool: true}
        subsystem: {string: drm}
        vendor: {string: "8086"}
    - name: pci-0000-00-02-0-display
      attributes:
        address: {string: "0000:00:02.0"}
        bus: {string: pci}
        class: {string: display}
        classCode: {string: "030000"}
        displayNode: {bool: true}
        driver: {string: i915}
        modalias: {string: "pci:v00008086d000046D2..."}
        name: {string: Alder Lake-N [UHD Graphics]}
        product: {string: 46d2}
        subsystem: {string: drm}
        vendor: {string: "8086"}
    - name: pci-0000-00-02-0-i2c-dev
      attributes:
        address: {string: "0000:00:02.0"}
        bus: {string: pci}
        class: {string: display}
        classCode: {string: "030000"}
        driver: {string: i915}
        modalias: {string: "pci:v00008086d000046D2..."}
        name: {string: Alder Lake-N [UHD Graphics]}
        product: {string: 46d2}
        subsystem: {string: i2c-dev}
        vendor: {string: "8086"}

This machine publishes three devices for one GPU.

* `pci-0000-00-02-0` delivers the render node,
  `/dev/dri/renderD128`. More than one claim can allocate it, so
  several workloads encode, decode, and compute on this GPU at the
  same time.
* `pci-0000-00-02-0-display` delivers the card node,
  `/dev/dri/card0`, which provides modesetting. One claim allocates it,
  because one process at a time can drive a display.
* `pci-0000-00-02-0-i2c-dev` delivers the i2c monitor-control buses
  that i915 registers for each display output. One claim allocates it,
  because those buses are raw wires.

All three have the same `address`, because they are one card. A claim
uses that to ask for halves of the same GPU. See
[Two requests, one card](#two-requests-one-card).

If the hardware you expect is not in the list, no driver is bound to
it. Look at the hardware the machine reports that it cannot drive:

    kubectl get machine <name> -o jsonpath='{.status.hardware.unclaimed}'

## 2. Declare the driver

A device becomes available for a claim when a driver binds it. Add the
module to [`spec.modules`](/docs/reference/machine/#spec--modules)
in the machine's manifest:

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

`liken` publishes an attribute only when it is true of the hardware, so
`has()` is the complete test. Guard an attribute a device may lack
with `has()` before you read it. The Kubernetes API treats an
unguarded read of a missing attribute as an evaluation error, and an
evaluation error aborts the whole allocation.

Do not select on `driver` alone, and do not select on `subsystem`
alone. Every device that `liken` publishes for one GPU has the same
`driver`, and the card node has the same `subsystem: drm` as the
render node. A class that matches more than one of them can allocate
the monitor buses, or the display, to your transcoder. `renderNode`
is the fact that names the half you want, and only that half has
it.

This class matches any GPU with a DRM render node, on any machine in
the fleet. A selector that asks for a vendor and a product ID names
one model, and it stops matching on the next machine you buy.

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

The container receives every node the published device delivers. For
the GPU above, that is `/dev/dri/renderD128` only. The card node and
the `/dev/i2c-*` monitor-control nodes belong to the companion
devices, and each companion needs its own request. Nothing else
changes: no privilege, and no host mount. Your image supplies the
userspace driver, and the image's user must be able to open the node.
Check what the container received:

    kubectl exec deploy/transcoder -- ls -l /dev/dri

A device that has `allowMultipleAllocations: true` serves more
than one claim: a second deployment writes its own `ResourceClaim`
against the same `DeviceClass`, and both deployments run. The
integrated GPU above has it, so more than one transcoder can
allocate the render node at once.

Give a deployment that holds a claim like this the `Recreate`
strategy. A rolling update runs the old pod and the new pod at once,
both name the same claim, and Kubernetes gives a claim's device to
every pod that names the claim. `Recreate` stops the old pod first,
so one pod holds the device at a time.

### A workload that drives a display

A player or a kiosk sets the video mode, so it needs the card node as
well. Write a second `DeviceClass` for the display half:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: gpu-display
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "liken.sh" &&
              has(device.attributes["liken.sh"].displayNode)

Then put both requests in one claim. The container receives the nodes
of both devices:

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: player-gpu
      namespace: media
    spec:
      devices:
        requests:
          - name: render
            exactly:
              deviceClassName: gpu-render
          - name: display
            exactly:
              deviceClassName: gpu-display

The display device allocates to one claim, so a second player waits
until the first claim ends. This is the correct behavior: the kernel
gives modesetting to one process for each card, and a second player
that started would fail when it opened the card node.

Sound is a third device, and a different one: the audio controller
has its own PCI address, so a player that plays HDMI audio adds a
request for it. That device is shareable, so a second player does not
wait for it.

### Two requests, one card

On a machine with two GPUs, the two requests above can allocate halves
of different cards. A `constraints` block pairs them. Each constraint
names the requests it applies to and one attribute. The scheduler
then allocates devices whose value for that attribute is the same.
The attribute to use is `address`, which is the device's address on
its bus:

    spec:
      devices:
        requests:
          - name: render
            exactly:
              deviceClassName: gpu-render
          - name: display
            exactly:
              deviceClassName: gpu-display
        constraints:
          - requests: [render, display]
            matchAttribute: liken.sh/address

Every device that `liken` publishes for one card has that card's
address, and two cards in a machine have different addresses. The
render node and the card node the claim allocates are now halves of
the same GPU.

Leave the audio controller out of the list. It has an address of its
own, so a constraint that included its request would never match.

## 5. Give one pod a device alone

Most devices are not shareable. A USB adapter has one control
endpoint, so `liken` publishes it as a device that allocates one time.
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
that names it, by design, and `liken` does not refuse the second pod. A
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
   reads an attribute the device does not have errors on that device
   and aborts the allocation, so it never matches. Guard the reference
   with `has()` so an absent attribute means "does not match" instead.
3. If the device is published and it matches, another claim can
   already hold it. Find the holder:

       kubectl get resourceclaims -A -o wide

   A device that is not shareable allocates one time, and the second
   claim waits until the first claim releases it.
