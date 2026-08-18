---
title: Pair a controller and give it to a pod
weight: 20
---

# Pair a controller and give it to a pod

This guide pairs a game controller with `kubectl` and hands it to one
pod. The example is a DualSense and a game in a namespace named
`arcade`, on a [`liken`](https://liken.sh/docs/) cluster with
[the operator installed](/docs/guides/install/). Every step is a Kubernetes
API call, so RBAC controls who may do each one, and nobody needs a
shell on a node or in a pod.

## 1. Open a pairing window

Read the name of the adapter first. It is the radio's address in
lowercase with dashes:

    kubectl get adapters

Then create a `PairingRequest` for it:

    kubectl apply -f - <<'EOF'
    apiVersion: bluetooth.liken.sh/v1alpha1
    kind: PairingRequest
    metadata:
      name: new-gamepad
      namespace: liken-system
    spec:
      adapter: 04-4a-69-66-92-27
      windowSeconds: 180
    EOF

The operator opens a window on that radio: it scans, and it stays
pairable and discoverable, for `windowSeconds` (180 by default, 15 to
900). Between windows the radio is neither, so nothing pairs with the
cluster while nobody asked.

## 2. Put the controller in pairing mode and read what the radio reports

On a DualSense, hold **Create** and **PS** until the light bar
flashes. Then read the request:

    kubectl get pairingrequest new-gamepad -n liken-system -o yaml

Every device the scan finds, and the cluster holds no bond with,
appears in `status.seen` with its address, its name, and when the
radio first observed it.

## 3. Approve the device you meant

Approval is a write to the request's spec:

    kubectl patch pairingrequest new-gamepad -n liken-system \
      --type merge -p '{"spec":{"device":"A0:AB:51:33:B7:12"}}'

The operator pairs that device, trusts it so it reconnects on its
own, records the bond as a `Pairing`, and closes the window. The
request's `status.phase` goes to `Paired`. A request nobody approves
only scans: an empty `spec.device` never pairs anything, the window
expires on its own, and the finished request is collected after
`spec.ttlSecondsAfterFinished`, a day by default.

To re-pair a device the cluster already records, set `spec.device`
when you create the request. An address set at creation is an
approval in advance.

## 4. See the published device

The bond is now a `Pairing`, its keys are in a `Secret` the `Pairing`
owns, and the controller is a device in this node's `ResourceSlice`:

    $ kubectl get resourceslice liken-1-bluetooth.liken.sh -o yaml
    spec:
      driver: bluetooth.liken.sh
      nodeName: liken-1
      devices:
        - name: a0-ab-51-33-b7-12
          attributes:
            address: {string: "A0:AB:51:33:B7:12"}
            connected: {bool: true}
            name: {string: "DualSense Wireless Controller"}

From here on, **PS** alone reconnects the controller. The keys are
in the `Secret`, so they survive a pod restart, an upgrade, and a
reboot.

## 5. Claim the controller

If the [Dynamic Resource Allocation
(DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
objects are new to you, read
[How the pieces fit](/docs/guides/#how-the-pieces-fit) first. Then
create a
[`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)
that selects the controller by its address:

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: player-one
      namespace: arcade
    spec:
      devices:
        requests:
          - name: controller
            exactly:
              deviceClassName: bluetooth-controller
              selectors:
                - cel:
                    expression: |
                      device.attributes["bluetooth.liken.sh"].address == "A0:AB:51:33:B7:12"
              tolerations:
                - key: bluetooth.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

The toleration sets how long the radio may go silent before the
eviction controller ends the pod. Tolerate
`bluetooth.liken.sh/disconnected` and nothing else:
[Devices](/docs/reference/devices/#the-taints) explains why the other
taint must stay untolerated. Leave out the selector to claim any
paired controller.

## 6. Give the claim to a pod

    apiVersion: v1
    kind: Pod
    metadata:
      name: player
      namespace: arcade
    spec:
      resourceClaims:
        - name: controller
          resourceClaimName: player-one
      containers:
        - name: game
          image: ...
          resources:
            claims:
              - name: controller

The container receives device nodes and nothing else:
`/dev/input/event*` for the one controller the claim allocated, which
on a DualSense is the gamepad and its motion sensors. No privilege,
no host mount, no environment variable. The container's user must be
able to open the nodes.

If the controller is switched off, the pod parks `Unschedulable` and
starts when somebody turns it on. If the controller disconnects while
the pod runs, the eviction after `tolerationSeconds` ends the pod's
session.

In a `Deployment`, claim through a `ResourceClaimTemplate` instead of
a standing `ResourceClaim`. A standing claim keeps its allocation
across an eviction, so the `ReplicaSet`'s replacement pods would
schedule onto a device that is gone and be evicted at once. A
template gives each replacement pod a fresh claim, and a fresh claim
needs a new allocation, which the taints block.

## Unpair

Deleting the `Pairing` is the unpair:

    kubectl delete pairing a0-ab-51-33-b7-12

The operator disconnects the controller, waits for any claim on it to
release, retires the device from the slice, and removes the bond. The
`Secret` with the keys is owned by the `Pairing`, so it is collected
with the object.
