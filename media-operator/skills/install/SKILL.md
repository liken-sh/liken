---
name: install
description: "Install media-operator and its message bus on a liken cluster, choose the device classes, and watch it start. Use when a cluster has no Player, Play, Remote, Keymap, or MediaPreferences yet, when running a development build, or when removing the operator."
---

This skill is the guide at https://liken.sh/media/docs/guides/install/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Install the operator

This guide installs `media-operator` on a
[`liken`](https://liken.sh/docs/) cluster. At the end, the operator
and its message bus run in `liken-system`, and the cluster accepts
the five resources: `Player`, `Play`, `Remote`, `Keymap`, and
`MediaPreferences`. On each node with a GPU, the capabilities agent
publishes what the GPU's media driver can decode, encode, and scale.

You need:

* A `liken` cluster.
* The hardware operators for the devices your players will select:
  the [`display-operator`](https://liken.sh/display/) for a screen,
  the [`audio-operator`](https://liken.sh/audio/) for sound, and the
  [`bluetooth-operator`](https://liken.sh/bluetooth/) for controllers
  and Bluetooth speakers. Install the ones your equipment has; a
  `Player` can only select what an installed operator publishes.
* `kubectl` with cluster-admin access, because the install creates
  the CRDs, the `DeviceClass` objects, and the `ClusterRoles`.

## The device classes are yours

The classes a `Player` names are the cluster owner's vocabulary, the
same classes a hand-written `ResourceClaim` would use, and the install
manifests do not define them. Each hardware operator's manual gives
the YAML for its class:
[displays](https://liken.sh/display/docs/guides/install/),
[audio outputs](https://liken.sh/audio/docs/guides/install/), and
[Bluetooth devices](https://liken.sh/bluetooth/docs/guides/install/).

The install defines one class, for the capabilities agent alone.
`capabilities.yaml` defines `media-render`, the class of the agent's
own claim on the render nodes. A workload that claims a GPU by what its
driver states needs classes that the cluster owner writes.
[Claim a GPU by what it decodes](https://liken.sh/media/docs/guides/claim-a-gpu-by-capability/)
gives the YAML.

## Apply the manifests

This site serves the repository's
[`deploy/`](https://liken.sh/media/deploy/kustomization.yaml) directory as raw YAML, so
the install needs no clone:

    kubectl apply -n liken-system \
      -f https://liken.sh/media/deploy/players-crd.yaml \
      -f https://liken.sh/media/deploy/plays-crd.yaml \
      -f https://liken.sh/media/deploy/remotes-crd.yaml \
      -f https://liken.sh/media/deploy/keymaps-crd.yaml \
      -f https://liken.sh/media/deploy/mediapreferences-crd.yaml \
      -f https://liken.sh/media/deploy/rbac.yaml \
      -f https://liken.sh/media/deploy/operator.yaml \
      -f https://liken.sh/media/deploy/api.yaml \
      -f https://liken.sh/media/deploy/bus.yaml \
      -f https://liken.sh/media/deploy/capabilities.yaml

The `-n` flag places the `ServiceAccounts`, the three `Deployments`,
the `DaemonSet`, and the `Services` in `liken-system`, the namespace
every `liken` cluster has. The CRDs, the `DeviceClasses`, and the
`ClusterRoles` are cluster-scoped, so the flag does not apply to them.

For GitOps, point a `Kustomization` at the served URLs. `kustomize`
takes a raw YAML URL as a resource:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    namespace: liken-system
    resources:
      - https://liken.sh/media/deploy/players-crd.yaml
      - https://liken.sh/media/deploy/plays-crd.yaml
      - https://liken.sh/media/deploy/remotes-crd.yaml
      - https://liken.sh/media/deploy/keymaps-crd.yaml
      - https://liken.sh/media/deploy/mediapreferences-crd.yaml
      - https://liken.sh/media/deploy/rbac.yaml
      - https://liken.sh/media/deploy/operator.yaml
      - https://liken.sh/media/deploy/api.yaml
      - https://liken.sh/media/deploy/bus.yaml
      - https://liken.sh/media/deploy/capabilities.yaml

A clone works too: `kubectl apply -k media-operator/deploy/` from the
repository applies the same files through
[`deploy/kustomization.yaml`](https://liken.sh/media/deploy/kustomization.yaml).

## Running a development build

A push to `main` that changes the operator publishes a development
build of it. Its version is the most recent release tag plus a suffix:
`2026.09.03-007-dev-003-abcdef01` is three commits past release
`2026.09.03-007`, at commit `abcdef01`. Every image of the operator
has the same version, and `:latest` still names the most recent
release.

A development build has no git tag, so the manifests pin to the
commit's full sha, and the image pins to the version:

```yaml
resources:
  - https://github.com/liken-sh/liken//media-operator/deploy?ref=<full 40-character sha>
images:
  - name: ghcr.io/liken-sh/media-operator
    newTag: 2026.09.03-007-dev-003-abcdef01
  - name: ghcr.io/liken-sh/media-operator-api
    newTag: 2026.09.03-007-dev-003-abcdef01
  - name: ghcr.io/liken-sh/media-operator-capabilities
    newTag: 2026.09.03-007-dev-003-abcdef01
```

A git fetch by sha needs all forty characters; the eight in the
version are not enough. The summary of the CI run for that commit
gives the version.

The same build publishes the manifests as the OCI artifact
`oci://ghcr.io/liken-sh/media-operator-deploy:<version>`, with each
image of the operator set to that version. A Flux `OCIRepository`
can pull that artifact by the version, with no sha.

## What the install runs

The install runs three `Deployments` and one `DaemonSet` in
`liken-system`, and they are separate on purpose:

* `media-operator` watches the five resources and reconciles them
  into claims and pods. It keeps no state on a volume. It serves no
  API. Its only listener answers Prometheus metrics on port 9200. On
  every pass it re-derives everything from the API server, and it
  reads each playback pod's report from the bus. Only the copy that
  holds the `Lease` named `media-operator` in `liken-system`
  reconciles, so a second copy from a rollout or a larger replica
  count waits and changes nothing.
* `media-api` answers HTTPS for a `Player`: the capture routes of
  [Record what a player is playing](https://liken.sh/media/docs/guides/record/) and the
  [API reference](https://liken.sh/media/docs/reference/api/). It runs from its own image,
  `media-operator-api`, which holds `ffmpeg`, and it has one replica
  with a `Recreate` strategy, so two pods never run at once. A
  `Service` named `media-api` serves it on port 443.
* `bus` is one [Mosquitto](https://mosquitto.org/) broker, with a
  `Service` at `bus.liken-system.svc:1883`. The broker is not inside
  the operator's pod, so the operator restarts without dropping a
  message, and a button press reaches `mpv` while the operator is
  down.
* `media-capabilities` is the capabilities agent, one pod on each node
  with a GPU. It holds a shareable claim on every render node of its
  node, asks each one's VA-API driver what it supports, and publishes
  a `media.liken.sh` device for each GPU. It also registers the
  `media.liken.sh` DRA driver with the node's kubelet, which a pod
  whose claim holds one of those devices needs before it starts.
  [Render node capabilities](https://liken.sh/media/docs/reference/capabilities/) describes
  the devices.

## Container resources

Every container the operator builds states a cpu request, a memory
request, and a memory limit, and none states a cpu limit. The defaults
fit a 1GB machine that drives a 1920x1080 screen. The requests are near
the steady use measured on a home cluster at 1920x1080, and each memory
limit is above the highest use measured at 3840x2160, with about half
again as headroom.

| Container | Pod | cpu request | memory request | memory limit |
|---|---|---|---|---|
| `player` | playback | 80m | 432Mi | 1Gi |
| `display` | playback | 10m | 144Mi | 640Mi |
| `command` | playback | 10m | 16Mi | 32Mi |
| `idle` | idle | 5m | 96Mi | 256Mi |
| `reader` | a `Remote`'s pod | 1m | 4Mi | 16Mi |

The values come from the `ConfigMap` `media-operator-resources`, which
the base generates from
[`deploy/container-resources.yaml`](https://github.com/liken-sh/liken/blob/main/media-operator/deploy/container-resources.yaml).
The operator reads it once at start. The generator adds a hash of the
content to the `ConfigMap`'s name, so a changed value restarts the
operator, and the pods it creates after that carry the new value. The
idle pod and each `Remote`'s pod are recreated once with it. A playback
pod keeps its values until its film ends.

To change a value, copy `deploy/container-resources.yaml` into your
overlay, change the value, and merge the file into the base's
`ConfigMap`. The file's key must be `resources.yaml`. This overlay
raises the player's memory limit for a machine that plays large films:

    # kustomization.yaml
    resources:
      - https://github.com/liken-sh/liken//media-operator/deploy?ref=<tag>
    configMapGenerator:
      - name: media-operator-resources
        behavior: merge
        files:
          - resources.yaml=container-resources.yaml

    # container-resources.yaml, copied from the base, with one change
    player:
      requests: {cpu: 80m, memory: 432Mi}
      limits: {memory: 1536Mi}
    ...

A value that your file leaves out, or that is not a Kubernetes
quantity, takes the operator's default, and the operator logs one line
for it at start:

    media.liken.sh: container resources: player: limits.memory is unset; using the default 1Gi

A `limits.cpu` is ignored with one line of its own. A memory request
above its limit takes the defaults for both, because the API server
refuses such a pod.

## Watch it start

    kubectl -n liken-system get pods

The operator's pod, the API's pod, and the bus's pod report `Running`, and so does a
`media-capabilities` pod on each node with a GPU. The operator's log names the `Lease` it
holds, and then counts what it found. client-go's leader election
writes lines of its own beside these:

    kubectl -n liken-system logs deploy/media-operator
    media.liken.sh: holding lease liken-system/media-operator as media-operator-7d9f8b6c5d-x2k4p_1a2b3c4d
    media.liken.sh: operating 0 plays and 0 remotes over bus.liken-system.svc:1883

A copy that logs `waiting for lease liken-system/media-operator`
instead is a second copy. It takes over within about 11 seconds when
the holder shuts down, and 30 to 41 seconds after the holder's last
renewal when the holder stops without a shutdown.

The capabilities agent's log has one line for each GPU, with the
driver's name and every capability it publishes:

    kubectl -n liken-system logs ds/media-capabilities
    capabilities: pci-0000-00-02-0 (/dev/dri/renderD128, Intel iHD driver for Intel(R) Gen Graphics - 25.2.3 ()): decodeH264=true decodeHEVCMain=true ...
    slice: created node-1-media.liken.sh with 1 devices

From here, the work is declaring resources. The
[reference](https://liken.sh/media/docs/reference/) describes each one, and
[the message bus](https://liken.sh/media/docs/reference/bus/) describes every topic the
pods and your own programs share.

## Keep the capabilities pods off nodes with no GPU

The `DaemonSet` makes a pod on every node. On a node with no GPU, the
claim matches no device, and the pod stays `Pending`. To make no pod
on such a node, label the node `media.liken.sh/gpu: none`.

The `DaemonSet` in the base carries this node affinity, so no patch is
needed:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: media.liken.sh/gpu
              operator: NotIn
              values: ["none"]
```

`NotIn` matches a node whose label has a different value, and also a
node that has no such label. The Kubernetes page on
[set-based requirements](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#set-based-requirement)
gives this rule. So with no label the `DaemonSet` makes a pod on every
node, and a node labeled `none` gets no pod. When a label changes, the
[`DaemonSet`](https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/)
controller deletes the pod from a node that no longer matches, and
adds one to a node that matches again.

Declare the label in the node's `Machine`, so the label is part of
the record of the machine:

    spec:
      nodeLabels:
        media.liken.sh/gpu: none

A `Machine` accepts a key in a `liken.sh` subdomain from `liken`
2026.09.28-002 on, and an older release refuses it, so on an older
release use the `kubectl label` way below.

The `liken` machine operator applies the label to the running node. A
machine that is demoted or installed again registers a new Node, and
the Node has the label from registration. To run the pod on that
node again, remove the key from `spec.nodeLabels`, and `liken`
removes the label from the node. `liken` also removes a label that
you set with `kubectl` before the `Machine` declared it, when the key
leaves the spec.

You can also label the node with `kubectl`:

    kubectl label node node-1 media.liken.sh/gpu=none

The label stays on the node across reboots. `liken` leaves it in
place, because `liken` removes only the labels that a `Machine`
declared. The label goes with the Node object: a machine that is
demoted or installed again registers a new Node, and you label it
again. To run the pod on that node again, remove the label:

    kubectl label node node-1 media.liken.sh/gpu-

A patch of your own that sets a node affinity on this `DaemonSet`
replaces the list of terms in the base, and the `none` term with it.
Copy the `none` requirement into each term of your patch.

## Read the player's full output

The playback pod runs `mpv` with `--quiet`, because `mpv`'s status
line prints about eight times a second and every line lands in the
pod log. Warnings and errors still print. To read everything `mpv`
says, set one variable on the operator:

```sh
kubectl set env deployment/media-operator MEDIA_PLAYER_VERBOSE=1
```

The switch removes `--quiet` and adds nothing else. It applies to every
playback pod created after it, so it takes effect on the next `Play`.
A pod already running keeps the setting it started with. Read the
log with `kubectl logs <play>-playback -c player`. To turn the switch
off again:

```sh
kubectl set env deployment/media-operator MEDIA_PLAYER_VERBOSE-
```

## Remove the operator

Deleting a `Play` stops its run. Deleting a `Player` or a
`Remote` removes its pods and claims, through the
`ownerReference` every one of them has. To remove the operator
itself:

    kubectl delete -n liken-system \
      -f https://liken.sh/media/deploy/rbac.yaml \
      -f https://liken.sh/media/deploy/operator.yaml \
      -f https://liken.sh/media/deploy/api.yaml \
      -f https://liken.sh/media/deploy/bus.yaml \
      -f https://liken.sh/media/deploy/capabilities.yaml

**Deleting a CRD deletes every resource of that kind.** Delete the
five `*-crd.yaml` files only when every player, play, remote,
keymap, and preference in the cluster can be deleted with them.
