---
title: Give a workload a device
weight: 70
---

# Give a workload a device

This guide gives a pod a piece of hardware: a GPU shared between
deployments, and a USB adapter held by one pod alone. Nothing here
needs a privileged pod or a host path.

[Devices](/docs/reference/devices/) describes what liken publishes
and why.

## 1. See what a node offers

    kubectl get resourceslices
    kubectl get resourceslice <node>-liken.sh -o yaml

Each entry is one claimable device:

    - name: pci-0000-00-02-0
      allowMultipleAllocations: true
      attributes:
        bus: {string: pci}
        class: {string: display}
        classCode: {string: "030000"}
        driver: {string: i915}
        modalias: {string: "pci:v00008086d000046D1..."}
        name: {string: Alder Lake-N [UHD Graphics]}
        product: {string: 46d1}
        renderNode: {bool: true}
        subsystem: {string: drm}
        vendor: {string: "8086"}

If the hardware you expect is missing, no driver is bound to it. Look
at what the machine says it cannot drive:

    kubectl get machine <name> -o jsonpath='{.status.hardware.unclaimed}'

## 2. Declare the driver

A device becomes claimable when a driver binds it. Add the module to
the machine's manifest:

    spec:
      modules:
        - i915

Apply it, and the machine loads the module. Most modules load
without a reboot; the Machine's `SpecConverged` condition says when
the change has taken effect. The device appears in the node's slice
within a few seconds.

The hardware report names these modules for you, commented out, when
you install the machine. See
[Install a cluster](/docs/guides/install/#first-run-the-hardware-report).

## 3. Say what your workload needs

A `DeviceClass` is a named question about hardware. Write it once,
per kind of device your deployments ask for.

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
`has()` is the whole test. This class matches any GPU with a render
node, on any machine in the fleet. Asking for a vendor and product ID
instead would name one model, and stop matching on the next machine
you buy.

## 4. Share a GPU between deployments

A GPU with a render node may be allocated to more than one claim, so
each deployment writes its own claim and none of them know about each
other.

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

The pod names the claim, and each container that wants the device
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

The container receives `/dev/dri/card0` and `/dev/dri/renderD128`,
and nothing else changes: no privilege, no host mount. Your image
supplies the userspace driver, and its user must be able to open the
node. Check what arrived:

    kubectl exec deploy/transcoder -- ls -l /dev/dri

A second deployment in another namespace writes its own `ResourceClaim`
against the same `DeviceClass`, and both run. The hardware divides
itself between them.

## 5. Give one pod a device alone

Most devices are not shareable. A USB adapter has one control
endpoint, so liken publishes it as a device that allocates once.
Select it by what it is:

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

Add `device.attributes["liken.sh"].serial == "..."` when a machine
has two of the same adapter and it matters which one you get. The
device's name follows the port, not the unit, so a replacement
adapter in the same port answers to the same name.

Then let each pod mint its own claim:

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

A template makes one claim per pod, and the device allocates to one
claim, so exactly one pod holds the adapter. A rolling update waits
for the old pod to release it, which is what you want for hardware
that one process must own.

Do not point two pods at one `ResourceClaim` for a device like this.
Both would receive it. Kubernetes shares a claim among every pod that
names it, on purpose, and liken does not refuse the second pod: a
device node is not what grants exclusive access, and the kernel is
what enforces it. The template is how you say "this pod, alone".

## When a claim does not schedule

A pod stuck in `Pending` with an unallocated claim usually means no
device matched. Work through it in this order:

1. `kubectl get resourceslices -o yaml` — is the device published at
   all? If not, no driver is bound: go back to step 2.
2. Compare your selector against the device's attributes. A selector
   naming an attribute the device does not carry matches nothing.
3. If the device is published and matches, it may already be
   allocated. Check who holds it:

       kubectl get resourceclaims -A -o wide

   A device that is not shareable allocates once, and the second
   claim waits until the first is released.
