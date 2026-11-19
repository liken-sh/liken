---
title: Give a workload a device
weight: 70
description: "Give a pod a device through Dynamic Resource Allocation: a GPU for a Deployment, a USB adapter that one pod holds alone, or /dev/kvm for a pod that boots virtual machines. Use when a workload needs hardware, or when a pod with a device claim does not schedule."
---

# Give a workload a device

This guide gives a pod a piece of hardware: a GPU for a transcoder,
a USB adapter that one pod holds alone, and `/dev/kvm` for a pod that
boots virtual machines. No step here needs a
privileged pod or a host path.

[Devices](/docs/reference/devices/) describes what `liken` publishes and
why. The [device operators](/docs/concepts/claiming-hardware/)
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
        resource.kubernetes.io/pciBusID: {string: "0000:00:02.0"}
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
        resource.kubernetes.io/pciBusID: {string: "0000:00:02.0"}
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
        resource.kubernetes.io/pciBusID: {string: "0000:00:02.0"}
        subsystem: {string: i2c-dev}
        vendor: {string: "8086"}

This machine publishes three devices for one GPU.

* `pci-0000-00-02-0` provides the render node,
  `/dev/dri/renderD128`. More than one claim can allocate it, so
  several workloads encode, decode, and compute on this GPU at the
  same time.
* `pci-0000-00-02-0-display` provides the card node,
  `/dev/dri/card0`, which sets the display mode. Only one claim can hold it,
  because one process at a time can drive a display.
* `pci-0000-00-02-0-i2c-dev` provides the i2c monitor-control buses
  that i915 registers for each display output. Only one claim can hold it,
  because two writers on one of those buses have no arbitration.

All three have the same `address`, because they are one card. A claim
can use that to request two devices of the same GPU. See
[Two requests, one card](#two-requests-one-card).

If the hardware you expect is not in the list, usually no driver is
bound to it. Look at the hardware the machine reports that it cannot
drive:

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
when the machine has applied the change, and the device appears in the
node's slice a few seconds later.

The hardware report lists these modules for you, as comments, when you
install the machine. See
[Install a cluster](/docs/guides/install/#first-run-the-hardware-report).

Declare the `uhid` module on a machine whose Bluetooth adapter serves
BLE input devices, such as a remote or a keyboard. The adapter's claim
then provides `/dev/uhid`. The
[device reference](/docs/reference/devices/#bluetooth-adapters)
explains why.

A USB-CEC adapter needs a `spec.serio` entry beside its modules,
because its driver binds only after the machine attaches the
adapter's serial line.
[Attach a USB-CEC adapter](/docs/guides/hardware-modules/#attach-a-usb-cec-adapter)
gives the steps, and the
[device reference](/docs/reference/devices/#serial-line-adapters)
describes the two devices the adapter publishes.

## 3. Say what your workload needs

A `DeviceClass` is a named set of selectors that match devices. Write
one for each kind of device that your deployments request.

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
`has()` alone is enough to test for it. Guard an attribute a device may lack
with `has()` before you read it. The Kubernetes API treats an
unguarded read of a missing attribute as an evaluation error, and an
evaluation error aborts the whole allocation.

Do not select on `driver` alone, and do not select on `subsystem`
alone. Every device that `liken` publishes for one GPU has the same
`driver`, and the card node has the same `subsystem: drm` as the
render node. A class that matches more than one of them can allocate
the monitor buses, or the display, to your transcoder. Only the render
node has the `renderNode` attribute, so select on it.

This class matches any GPU with a DRM render node, on any machine in
the fleet. A selector on a vendor and a product ID matches
one model, and it stops matching on the next machine you buy.

## 4. Claim the GPU for a deployment

Write a `ResourceClaim` that requests a device of the class. The claim
also decides where the pod runs: the scheduler places the pod on a
machine whose `ResourceSlice` has a matching device.

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

The pod references the claim, and each container that needs the device
references the pod's entry:

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

The container receives every device node that the published device provides. For
the GPU above, that is `/dev/dri/renderD128` only. The card node and
the `/dev/i2c-*` monitor-control nodes belong to the companion
devices, and each companion needs its own request. The pod needs no
privilege and no host mount. Your image supplies the
userspace driver, and the image's user must be able to open the node.
Check what the container received:

    kubectl exec deploy/transcoder -- ls -l /dev/dri

Because the render node has `allowMultipleAllocations: true`, a second
deployment can write its own `ResourceClaim` against the same
`DeviceClass`, and both deployments run on the same GPU.

Give a deployment that holds a claim like this the `Recreate`
strategy. A rolling update runs the old pod and the new pod at once,
and both reference the same claim. Kubernetes gives a claim's device to
every pod that references the claim. `Recreate` stops the old pod first,
so one pod holds the device at a time.

### A workload that drives a display

A player or a kiosk sets the video mode, so it needs the card node as
well. Write a second `DeviceClass` for the display device:

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

Only one claim can hold the display device, so a second player waits
until the first claim ends. The kernel lets one process at a time set
the mode of a card, so a second player would fail when it opened the
card node.

A player that plays HDMI audio adds a request for the GPU's audio
controller, which has its own PCI address. That device is shareable,
so a second player does not wait for it.

### Two requests, one card

On a machine with two GPUs, the two requests above can allocate the
render node of one card and the card node of the other. Add a
`constraints` block. A constraint lists requests and one attribute,
and the scheduler allocates only devices that have the same value of
that attribute. Use `address`, the device's address on its bus:

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
render node and the card node that the claim allocates now belong to
the same GPU.

Leave the audio controller out of the list. It has an address of its
own, so a constraint that included its request would never match.

`liken.sh/address` pairs only the devices of `liken`. To pair the
render node with a device that another driver publishes for the same
card, such as the device that `media-operator` publishes to describe what a GPU decodes, use
`matchAttribute: resource.kubernetes.io/pciBusID`. Kubernetes defines
that attribute, and both drivers publish it with the card's PCI
address.

## 5. Give one pod a device alone

Most devices are not shareable. A USB adapter has one control
endpoint, so `liken` publishes it as a device that only one claim can hold.
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
              has(device.attributes["liken.sh"].vendor) &&
              has(device.attributes["liken.sh"].product) &&
              device.attributes["liken.sh"].vendor == "10c4" &&
              device.attributes["liken.sh"].product == "ea60"

A device on the board, such as a laptop's brightness keys, has no
`vendor` or `product`, and a selector that reads an attribute a device
does not have aborts the allocation, so guard each read with `has()`.
Add `has(device.attributes["liken.sh"].serial) &&
device.attributes["liken.sh"].serial == "..."` when a machine has two
adapters of the same model and you must select one of them. The
device's name comes from the USB port, not from the adapter, so a
replacement adapter in the same port has the same name.

Do not select on `busNumber` or `deviceNumber`. The kernel gives the
device a new device number each time it enumerates, after a replug, a
reset, or a firmware upload, so a claim that names one stops matching.
The [device reference](/docs/reference/devices/#usb-devices) says what
the two numbers are for.

Some USB devices, such as many astronomy cameras, hold no firmware of
their own and get it from the host each time they get power. Until
the firmware loads, the device appears as a bare chip with the vendor
and product IDs of the chip's maker. After the firmware loads, the
device appears again with its own IDs. Select those IDs.
[`firmware-operator`](https://liken.sh/firmware/docs/guides/upload-to-a-device/)
loads the firmware each time the bare chip appears.

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

When you unplug the adapter, `liken` taints it, and Kubernetes evicts
the pod. The pod's controller creates a new pod, and its new claim is
allocated the adapter wherever you plug it in: another port, or
another machine. This works only with a template. A named
`ResourceClaim` stays allocated to the device on the old port, and you
must repair it by hand.
The [device reference](/docs/reference/devices/#an-unplugged-device-evicts-its-pods)
gives the rules, and the toleration for a program that follows its
hardware by itself.

A template makes one claim for each pod, and only one claim can hold the
device, so only one pod holds the adapter. A rolling update waits
until the old pod releases the adapter.

Do not make two pods reference one `ResourceClaim` for a device of this
kind. Kubernetes gives a claim's devices to every pod that references
the claim, and `liken` does not refuse the second pod. The device node
does not give one pod exclusive access either. Only the kernel
enforces that, through `O_EXCL` and the driver's open function. Use a
template to give the device to one pod only.

## 6. Run a virtual machine in a pod

A pod that boots a virtual machine, with QEMU, Firecracker, or Cloud
Hypervisor, claims `/dev/kvm` the same way a transcoder claims a GPU,
and runs with no privilege. First declare the vendor module for the
machine's CPU in `spec.modules`, as
[Load the drivers](/docs/guides/hardware-modules/#run-virtual-machines)
describes. The node then publishes `misc-kvm`:

    kubectl get resourceslice <node>-liken.sh -o yaml

    - name: misc-kvm
      allowMultipleAllocations: true
      attributes:
        class: {string: virtualization}
        driver: {string: kvm_intel}
        kvm: {bool: true}
        nested: {bool: true}
        subsystem: {string: misc}

Write a class that selects it:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: kvm
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "liken.sh" &&
              has(device.attributes["liken.sh"].kvm)

Add `has(device.attributes["liken.sh"].nested)` for a guest that runs
virtual machines itself. Write classes for `vhostNet` and `vhostVsock`
the same way when the virtual machine monitor opens `/dev/vhost-net` or
`/dev/vhost-vsock`.

Claim it from a template, so each pod gets a claim of its own. The
device can be allocated to any number of claims, so pods on one node run
guests at the same time:

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaimTemplate
    metadata:
      name: kvm
      namespace: ci
    spec:
      spec:
        devices:
          requests:
            - name: kvm
              exactly:
                deviceClassName: kvm

    spec:
      template:
        spec:
          resourceClaims:
            - name: kvm
              resourceClaimTemplateName: kvm
          containers:
            - name: runner
              image: ...
              resources:
                requests:
                  memory: 3Gi
                claims:
                  - name: kvm

The container receives `/dev/kvm` with mode `0666`, so a virtual
machine monitor that runs as a user other than root can open it. A
guest's memory belongs to the monitor's process, so the container's
memory request and limit must cover the guest's memory and the
monitor's own.

A guest on a tap network also needs `/dev/net/tun`. containerd's
default device rules allow that node in every container, so it needs
no claim. Create the node in the container, and add the capability
that creates the tap interface:

    securityContext:
      capabilities:
        add: ["NET_ADMIN"]

    mknod /dev/net/tun c 10 200

A pod with `hostUsers: false` cannot open these nodes, because the
runtime mounts the machine's own node, mode `0600`, into a user
namespace. The
[device reference](/docs/reference/devices/#virtualization-devices)
gives the detail, how each pod gets vsock addresses of its own, and
why a pod that opens a vsock socket to its guest must not run under
the `RuntimeDefault` seccomp profile.

## When a claim does not schedule

If a pod stays in `Pending` with a claim that is not allocated, then
usually no device matched the claim. Do these checks in this order:

1. `kubectl get resourceslices -o yaml` shows if the device is
   published. If the device is not there, usually no driver is bound
   to it: go back to step 2. The
   [device reference](/docs/reference/devices/#what-a-node-publishes)
   lists every reason a device stays out of the slice. A USB device
   that is there with other vendor and product IDs than you expect can
   be a bare chip that waits for its firmware, as
   [step 5](#5-give-one-pod-a-device-alone) describes.
2. Compare your selector with the device's attributes. A selector that
   reads an attribute the device does not have errors on that device
   and aborts the allocation, so it never matches. Guard the reference
   with `has()` so an absent attribute means "does not match" instead.
3. If the device is published and it matches, another claim can
   already hold it. Find the holder:

       kubectl get resourceclaims -A -o wide

   A device that is not shareable can be allocated to only one claim, and the second
   claim waits until the first claim releases it.
