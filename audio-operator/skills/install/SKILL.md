---
name: install
description: "Install audio-operator on a liken cluster and confirm it publishes every physical audio output as a device. Use when a cluster has no audio devices yet, when running a development build, or when removing the operator."
---

This skill is the guide at https://liken.sh/audio/docs/guides/install/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Install the operator

This guide installs `audio-operator` on a
[`liken`](https://liken.sh/docs/) cluster. At the end, every physical
audio output on the cluster is a device that a workload can claim.

You need:

* A `liken` cluster. The operator claims the sound card through
  [Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/),
  from the devices that `liken`'s own driver publishes.
  [Devices](https://liken.sh/docs/reference/devices/) describes those
  devices.
* A machine in that cluster with a sound card: a monitor with speakers
  on HDMI or DisplayPort, or something wired to the analog jack.
* For Bluetooth speakers, the
  [`bluetooth-operator`](https://liken.sh/bluetooth/) on the same
  machine. Its media bus gives the sound server access to
  `bluetoothd`'s D-Bus socket. The `bluetooth-operator` is optional.
  A machine with a sound card and no radio needs nothing extra.
* `kubectl` with cluster-admin access. You create the cluster-scoped
  [`DeviceClasses`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
  for your workloads yourself, and the base creates cluster-scoped
  objects too: its
  `ClusterRole`s and `ClusterRoleBinding`s, and the
  `CustomResourceDefinitions` for `Sink` and `Source`.

## 1. Check that the card publishes

The machine with the speakers must publish its audio controller as a
device. Look in that node's `liken.sh` `ResourceSlice` for a device
with the attribute `sound.liken.sh/supportsSound: {bool: true}`. The
operator's `sound-card` class selects on that attribute:

    kubectl get resourceslice <node>-liken.sh -o yaml

If no device has the attribute, the operator's own claim cannot be
allocated, and its pod stays `Pending`.
[Claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/)
explains why: `liken` publishes the whole card, and this operator
claims it and publishes each of its outputs.

## 2. The device classes

A `DeviceClass` is cluster-scoped policy. You name and manage the
classes yourself, the same way you do `StorageClass`es.

* `sound-card`: the operator's own pod claims every sound device on
  its node through this class. The base provides it, in
  [`deviceclasses.yaml`](https://liken.sh/audio/deploy/deviceclasses.yaml). The claim
  template in [`operator.yaml`](https://liken.sh/audio/deploy/operator.yaml) names it, so
  the operator does not start without it. Do not delete it.
* You create the classes that your workloads claim through, and the
  base provides none. Start with `audio-sink` and `audio-source`. Each
  selects one direction: a playback endpoint has the `sink`
  attribute, and a capture endpoint has the `source` attribute:

        apiVersion: resource.k8s.io/v1
        kind: DeviceClass
        metadata:
          name: audio-sink
        spec:
          selectors:
            - cel:
                expression: has(device.attributes["audio.liken.sh"].sink)
        ---
        apiVersion: resource.k8s.io/v1
        kind: DeviceClass
        metadata:
          name: audio-source
        spec:
          selectors:
            - cel:
                expression: has(device.attributes["audio.liken.sh"].source)

### Generic or specific

You decide how much each class selects. The `audio-sink` class above
is generic. It matches every playback endpoint, so the list of classes
stays short, and each claim's CEL selector chooses the output. A
specific class has the selector itself. A claim then names the class
and needs no CEL, and you make the choice once, in the cluster policy
that you control:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: analog-jack
    spec:
      selectors:
        - cel:
            expression: |
              has(device.attributes["audio.liken.sh"].sink) &&
              has(device.attributes["audio.liken.sh"].connectionType) &&
              device.attributes["audio.liken.sh"].connectionType == "analog"

A class that selects the speakers of one monitor through
`monitor.liken.sh/id` works the same way, with the same `has()` guard
on the attribute.

Start with generic classes. Create a specific class when several
workloads repeat the same selector, or when you want the choice in
cluster policy and not in each workload's manifest.

## 3. Apply the manifests

This site serves the repository's
[`deploy/`](https://liken.sh/audio/deploy/kustomization.yaml) directory as raw YAML, so you
do not need a clone. Apply these files:

    kubectl apply -n liken-system \
      -f https://liken.sh/audio/deploy/crds.yaml \
      -f https://liken.sh/audio/deploy/deviceclasses.yaml \
      -f https://liken.sh/audio/deploy/rbac.yaml \
      -f https://liken.sh/audio/deploy/operator.yaml \
      -f https://liken.sh/audio/deploy/api.yaml

`audio-api` needs a `RoleBinding` in `kube-system`, and `-n` would make
`kubectl` refuse it. So that binding is in a separate file,
`api-authentication.yaml`, and not in `api.yaml`. Apply it without
`-n`:

    kubectl apply -f https://liken.sh/audio/deploy/api-authentication.yaml

`crds.yaml` has the `CustomResourceDefinitions` for `Sink` and
`Source`. The operator creates one of these resources for every
endpoint that it publishes. The [`Sink`](https://liken.sh/audio/docs/reference/sinks/) and
[`Source`](https://liken.sh/audio/docs/reference/sources/) references describe them.

`api.yaml` has `audio-api`: one `Deployment` and one `Service` for
the whole cluster, with the RBAC that the API needs. The
[listen guide](https://liken.sh/audio/docs/guides/listen/) depends on it. If your cluster
needs no capture API, you can leave this file out.

The `-n` flag puts the `ServiceAccount` and the `DaemonSet` in
`liken-system`, the namespace that every `liken` cluster has. The
subject of the `ClusterRoleBinding` names that namespace, so the
binding works only there. `DeviceClass` is cluster-scoped, so the flag
does not affect it.

Each pod claims every sound card on its node through the
`ResourceClaimTemplate` `every-sound-card`. The claim tolerates
`liken.sh/unplugged`, the taint that `liken` adds to a device when it
is unplugged while a claim has it. So when you unplug one USB card,
PipeWire keeps playing on the other cards and the pod does not
restart.

A template's spec cannot change. If you installed the operator before
the claim tolerated that taint, you have a template named `sound-card`
that no pod uses after you apply the new manifests. A `Kustomization`
with pruning deletes it. After a plain `kubectl apply`, delete it
yourself:

    kubectl delete -n liken-system resourceclaimtemplate sound-card

For GitOps, put your specific classes in a file of your own. Point a
`Kustomization` at that file and at the served URLs. `kustomize`
accepts a raw YAML URL as a resource:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    transformers:
      - |-
        apiVersion: builtin
        kind: NamespaceTransformer
        metadata:
          name: liken-system
          namespace: liken-system
        unsetOnly: true
    resources:
      - classes.yaml
      - https://liken.sh/audio/deploy/crds.yaml
      - https://liken.sh/audio/deploy/deviceclasses.yaml
      - https://liken.sh/audio/deploy/rbac.yaml
      - https://liken.sh/audio/deploy/operator.yaml
      - https://liken.sh/audio/deploy/api.yaml
      - https://liken.sh/audio/deploy/api-authentication.yaml

The transformer sets `liken-system` only on objects that have no
namespace, so the binding in `api-authentication.yaml` stays in
`kube-system`. A top-level `namespace:` field would move it, and the
API could not read the client certificate authority.

A clone works too. Run `kubectl apply -k audio-operator/deploy/` from
the repository to apply the same files through
[`deploy/kustomization.yaml`](https://liken.sh/audio/deploy/kustomization.yaml).

## 4. Watch the operator find the outputs

The operator runs as a `DaemonSet`, so a pod starts on every node, and
no manifest names the machine with the speakers. Each pod claims every
audio controller on its own node. On a node with no sound card, the
claim matches no device, and the pod stays `Pending`, which costs
nothing. A node labeled `audio.liken.sh/sound-card: none` gets no pod,
as
[Keep the pods off nodes with no sound card](#keep-the-pods-off-nodes-with-no-sound-card)
describes.

    kubectl -n liken-system get pods -o wide

Every container of the pod runs the same image. The `declare` init
container writes the PipeWire sink declarations. PipeWire and
WirePlumber run as sidecars. The `operator` container publishes what
they report, and the `capture` container serves the taps that
`audio-api` forwards to. On the machine with the card, the operator's
log shows the slice that it wrote:

    kubectl -n liken-system logs ds/audio-operator
    audio.liken.sh: operating the audio controller on kitchen
    slice: created generation 1, 3 devices, 0 tainted

The image is a file closure on `scratch`, with no shell and no package
manager. To inspect the running sound server, use `pw-dump`:

    kubectl -n liken-system exec ds/audio-operator -c operator -- pw-dump

When the graph looks correct and the sound is still wrong, use
`pw-top`, `pw-cli`, and `pw-metadata`, each with its own
`kubectl exec`. `pw-top -b -n 1` prints one reading of every node, and
its `ERR` column counts the dropouts. `pw-cli` lists any object in the
graph, and `pw-cli` with `set-param` writes a parameter on one without
a restart of the daemon. `pw-metadata` reads and writes the graph's
settings, for example `clock.force-quantum`.

    kubectl -n liken-system exec ds/audio-operator -c operator -- pw-top -b -n 1
    kubectl -n liken-system exec ds/audio-operator -c operator -- pw-cli info 0
    kubectl -n liken-system exec ds/audio-operator -c operator -- pw-metadata -n settings

## 5. See the devices

The operator publishes one device for each PCM device on the claimed
card, sinks and sources alike, in a `ResourceSlice` named
`<node>-audio.liken.sh`:

    kubectl get resourceslice <node>-audio.liken.sh -o yaml

Each endpoint is also a resource: one `Sink` for each playback
endpoint and one `Source` for each capture endpoint, with the same
names as the devices:

    kubectl get sinks
    NAME                                            NODE      CONNECTION   VOLUME   MUTE    CLAIMS   CONNECTED   READY   LAYOUT        LAYOUT SOURCE   AGE
    kitchen-pci-0000-00-1f-3-hdmi-0                 kitchen   hdmi         100      false            True        True    ["FL","FR"]   ELD             2m
    kitchen-usb-0573-1573-a34004801402-usb-audio    kitchen   usb          100      false            True        True    ["FL","FR"]   ChannelMap      2m

    kubectl get sources
    NAME                                                    NODE      CONNECTION   VOLUME   MUTE    CLAIMS   CONNECTED   READY   AGE
    kitchen-usb-0573-1573-a34004801402-usb-audio-capture    kitchen   usb          100      false            True        True    2m

When the monitor on an output responds, the device has the monitor's
attributes. An HDMI output with no monitor is published too, with
taints, so a claim on it stays `Pending` until a monitor responds.
[Devices](https://liken.sh/audio/docs/reference/devices/) describes every attribute.

When the pod's claim also allocated a Bluetooth media bus, the same
slice has one device for each paired Bluetooth speaker. A speaker that
is switched off is published with taints, the same way an HDMI output
with no monitor is.

Now [play sound to an output](https://liken.sh/audio/docs/guides/claim/), or
[set an endpoint's volume and controls](https://liken.sh/audio/docs/guides/rest/).

## Keep the pods off nodes with no sound card

The `DaemonSet` starts a pod on every node. On a node with no sound
card, the claim matches no device, and the pod stays `Pending`. To
start no pod on such a node, label the node
`audio.liken.sh/sound-card: none`.

The `DaemonSet` in the base has this node affinity, so you need no
patch:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: audio.liken.sh/sound-card
              operator: NotIn
              values: ["none"]
```

`NotIn` matches a node whose label has a different value, and it also
matches a node that has no such label. The Kubernetes page on
[set-based requirements](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#set-based-requirement)
describes this rule. So the `DaemonSet` starts a pod on every node
that has no label, and starts none on a node labeled `none`. When a
label changes, the
[`DaemonSet`](https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/)
controller deletes the pod from a node that no longer matches, and
creates a pod on a node that matches again.

Declare the label in the node's `Machine`, so that the record of the
machine includes it:

    spec:
      nodeLabels:
        audio.liken.sh/sound-card: none

A `Machine` accepts a key in a `liken.sh` subdomain from `liken`
2026.09.28-002 on, and an older release refuses it. On an older
release, use the `kubectl label` method below.

The `liken` machine operator applies the label to the running node. A
machine that you demote or install again registers a new Node, and the
new Node has the label from the start. To run the pod on that node
again, remove the key from `spec.nodeLabels`, and `liken` removes the
label from the node. `liken` also removes a label that you set with
`kubectl` before the `Machine` declared it, when the key leaves the
spec.

You can also label the node with `kubectl`:

    kubectl label node node-1 audio.liken.sh/sound-card=none

The label stays on the node across reboots. `liken` leaves it in
place, because `liken` removes only the labels that a `Machine`
declared. The label belongs to the Node object. A machine that you
demote or install again registers a new Node, and you label the new
Node again. To run the pod on that node again, remove the label:

    kubectl label node node-1 audio.liken.sh/sound-card-

If you patch this `DaemonSet` with a node affinity of your own, your
list of terms replaces the list in the base, including the `none`
term. Copy the `none` requirement into each term of your patch.

## Pin a release

The site serves the manifests of the current `main`, and the images in
the manifests name `:latest`. To pin a release, reference the
operator's `kustomize` base at the operator's version, which is the
release tag that last published it. The
[GitHub release](https://github.com/liken-sh/liken/releases) for each
tag lists every component and its version. One tag sets the version of
the manifests and of the image, so pin both to the same version:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    resources:
      - https://github.com/liken-sh/liken//audio-operator/deploy?ref=<version>
    images:
      - name: ghcr.io/liken-sh/audio-operator
        newTag: <version>

## Running a development build

A push to `main` that changes the operator publishes a development
build of it. Its version is the most recent release tag plus a suffix.
For example, `2026.09.03-007-dev-003-abcdef01` is three commits after
release `2026.09.03-007`, at commit `abcdef01`. Every image of the
operator has the same version, and `:latest` still names the most
recent release.

A development build has no git tag, so the manifests pin to the full
sha of the commit, and the image pins to the version:

```yaml
resources:
  - https://github.com/liken-sh/liken//audio-operator/deploy?ref=<full 40-character sha>
images:
  - name: ghcr.io/liken-sh/audio-operator
    newTag: 2026.09.03-007-dev-003-abcdef01
```

A git fetch by sha needs all forty characters, and the eight in the
version are not enough. The summary of the CI run for that commit
shows the version.

The same build publishes the manifests as the OCI artifact
`oci://ghcr.io/liken-sh/audio-operator-deploy:<version>`, with each
image of the operator set to that version. A Flux `OCIRepository` can
pull that artifact by the version, with no sha.

Do not set `targetNamespace` on a Flux `Kustomization` that applies
the base. Flux sets that namespace on every object, the way a
top-level `namespace:` field does. The `RoleBinding` in
`api-authentication.yaml` must stay in `kube-system`, where the
built-in `Role` that it binds is. If the binding moves out of
`kube-system`, it names no `Role`, and the API cannot read the client
certificate authority.

## Remove the operator

Delete the manifests. Then delete the slice on each node that
published one:

    kubectl delete -n liken-system \
      -f https://liken.sh/audio/deploy/rbac.yaml \
      -f https://liken.sh/audio/deploy/operator.yaml \
      -f https://liken.sh/audio/deploy/api.yaml
    kubectl delete -f https://liken.sh/audio/deploy/api-authentication.yaml
    kubectl delete resourceslice <node>-audio.liken.sh

The `Secret`s and the `ConfigMap` of the API stay after the
`Deployment` is deleted. Delete `audio-api-tls`, `audio-capture-server`,
and `audio-api-ca` in `liken-system` by hand.

The `DeviceClasses` also stay: `sound-card` from the base, and the
classes that you created for consumers. Delete them when no other
claim names them. Deleting `crds.yaml` also deletes every `Sink` and
`Source`, with the declarations that they contain:

    kubectl delete deviceclass sound-card audio-sink audio-source
    kubectl delete -f https://liken.sh/audio/deploy/crds.yaml

You delete the slice yourself because the operator never deletes it. If
a device left the slice while a claim still named it, the kubelet's
prepare call for that claim would never finish. So the
operator adds taints to devices instead of removing them, and the
slice stays after every pod is gone.
