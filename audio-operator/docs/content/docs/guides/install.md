---
title: Install the operator
weight: 10
---

# Install the operator

This guide installs `audio-operator` on a
[`liken`](https://liken.sh/docs/) cluster. At the end, every
physical audio output on the cluster is a device a workload can
claim.

You need:

* A `liken` cluster. The operator claims the sound card through
  [Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/),
  from the devices `liken`'s own driver publishes.
  [Devices](https://liken.sh/docs/reference/devices/) describes
  those.
* A machine in that cluster with a sound card: a monitor with
  speakers on HDMI or DisplayPort, or something wired to the analog
  jack.
* `kubectl` with cluster-admin access. You create two cluster-scoped
  [`DeviceClasses`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
  yourself, and the base creates a `ClusterRole`.

## 1. Check that the card publishes

The machine with the speakers must publish its audio controller as a
device. Look for a device whose `subsystem` attribute is `sound` in
that node's `liken.sh` `ResourceSlice`:

    kubectl get resourceslice <node>-liken.sh -o yaml

If no device has `subsystem: {string: sound}`, the operator's
own claim will park and its pod will stay `Pending`. The
[hardware operators](https://liken.sh/docs/concepts/hardware-operators/)
page describes this layering: `liken` publishes the card, and this
operator refines it into outputs.

## 2. The device classes

A `DeviceClass` is cluster-scoped policy, yours to name and curate,
the same convention a `StorageClass` follows. The base ships this
operator's two generic classes, served at
[`deviceclasses.yaml`](/deploy/deviceclasses.yaml), because their
selectors name only the drivers' own vocabulary and carry no
cluster's choice:

* `sound-card` is the bootstrap class. The operator's own pod claims
  the machine's sound card through it, from the devices `liken`
  publishes, and the claim template in the served
  [`operator.yaml`](/deploy/operator.yaml) names it literally, so
  the operator cannot start without it. If you give that class
  another name, patch the template to match.
* `audio-output` is the class your workloads claim. It covers every
  device this driver publishes:

        apiVersion: resource.k8s.io/v1
        kind: DeviceClass
        metadata:
          name: audio-output
        spec:
          selectors:
            - cel:
                expression: device.driver == "audio.liken.sh"

### Generic or specific

A class is the cluster's vocabulary for a kind of device, and you
choose its grain. `audio-output` above is generic: it matches every
audio output, it keeps the class list short, and it leaves the
choice of output to each claim's CEL selector. A specific class
holds the selector itself. A claim then names the class and writes
no CEL, and you make the choice once, in cluster policy you
control:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: analog-jack
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "audio.liken.sh" &&
              has(device.attributes["audio.liken.sh"].connectionType) &&
              device.attributes["audio.liken.sh"].connectionType == "analog"

A class that names one monitor's speakers through
`monitor.liken.sh/id` works the same way, with the same `has()`
guard on the attribute.

Start generic. When several workloads repeat the same selector, or
when you want the choice in cluster policy rather than in each
workload's manifest, create a specific class.

## 3. Apply the manifests

This site serves the repository's
[`deploy/`](/deploy/kustomization.yaml) directory as raw YAML, so
the install needs no clone. Three files are the rest of the install:

    kubectl apply -n liken-system \
      -f https://audio.liken.sh/deploy/deviceclasses.yaml \
      -f https://audio.liken.sh/deploy/rbac.yaml \
      -f https://audio.liken.sh/deploy/operator.yaml

The `-n` flag places the `ServiceAccount` and the `DaemonSet` in
`liken-system`, the namespace every `liken` cluster has. The
`ClusterRoleBinding`'s subject names that namespace, so the binding
only works there. `DeviceClass` is cluster-scoped, so the flag
leaves it alone.

For GitOps, put your specific classes in a file of your own and
point a `Kustomization` at it and at the served URLs. `kustomize`
takes a raw YAML URL as a resource:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    namespace: liken-system
    resources:
      - classes.yaml
      - https://audio.liken.sh/deploy/deviceclasses.yaml
      - https://audio.liken.sh/deploy/rbac.yaml
      - https://audio.liken.sh/deploy/operator.yaml

A clone works too: `kubectl apply -k deploy/` from the repository
applies the same files through
[`deploy/kustomization.yaml`](/deploy/kustomization.yaml).

## 4. Watch the operator find the outputs

The operator runs as a `DaemonSet`, so a pod lands on every node and
no manifest names the machine with the speakers. Each pod claims
every audio controller on its own node. On a node with no sound
card, the claim finds no device and the pod parks `Pending`, which
costs nothing.

    kubectl -n liken-system get pods -o wide

The pod is four containers from one image. A `declare` init
container writes PipeWire's sink declarations, PipeWire and
WirePlumber run as sidecars, and the operator publishes what they
hold. On the machine with the card, the operator's log reports the
slice it wrote:

    kubectl -n liken-system logs ds/audio-operator
    audio.liken.sh: operating the audio controller on kitchen
    slice: created generation 1, 3 devices, 0 tainted

The image is a file closure on `scratch`: no shell, no package
manager. `pw-dump` is the way to inspect the running sound server:

    kubectl -n liken-system exec ds/audio-operator -c operator -- pw-dump

## 5. See the devices

The operator publishes one device for each playback PCM device on
the claimed card, into a `ResourceSlice` named
`<node>-audio.liken.sh`:

    kubectl get resourceslice <node>-audio.liken.sh -o yaml

An output whose monitor answers publishes the monitor's attributes.
An HDMI output with no monitor publishes too, with taints, so a
claim on it parks until a monitor arrives.
[Devices](/docs/reference/devices/) describes every attribute.

Now [play sound to an output](/docs/guides/claim/).

## Remove the operator

Delete the manifests. Then delete the slice on each node that
published one:

    kubectl delete -n liken-system \
      -f https://audio.liken.sh/deploy/rbac.yaml \
      -f https://audio.liken.sh/deploy/operator.yaml
    kubectl delete resourceslice <node>-audio.liken.sh

The two `DeviceClasses` are yours, so this leaves them in place.
Delete them when no other claim names them:

    kubectl delete deviceclass sound-card audio-output

The second step is yours because the operator never deletes its
slice. A device that leaves the inventory while a claim still names
it strands the kubelet's prepare call. So the operator taints
devices instead of removing them, and the slice outlives every pod.
