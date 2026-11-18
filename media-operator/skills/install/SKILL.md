---
name: install
description: "Install media-operator and its message bus on a liken cluster, choose the device classes, and watch it start. Use when a cluster has no Player, Play, Remote, Keymap, or MediaPreferences yet, when running a development build, or when removing the operator."
---

This skill is the guide at https://liken.sh/media/docs/guides/install/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Install the operator

This guide installs `media-operator` on a
[`liken`](https://liken.sh/docs/) cluster. At the end, the operator
and its message bus run in `liken-system`, and the cluster accepts
`Player`, `Play`, `Remote`, `Keymap`, and `MediaPreferences`
resources. On each node with a GPU, the capabilities agent publishes
what the GPU's media driver can decode, encode, and scale.

You need:

* A `liken` cluster.
* The hardware operators for the devices your players will select:
  the [`display-operator`](https://liken.sh/display/) for a screen,
  the [`audio-operator`](https://liken.sh/audio/) for sound, and the
  [`bluetooth-operator`](https://liken.sh/bluetooth/) for controllers
  and Bluetooth speakers. A `Player` can select only the devices that
  an installed operator publishes.
* `kubectl` with cluster-admin access, because the install creates
  the CRDs, the `DeviceClass` objects, and the `ClusterRoles`.

## The device classes are yours

A `Player` selects its devices by `DeviceClass`, the same classes that
a hand-written `ResourceClaim` uses. You create them and choose their
names, because the install manifests do not. Each hardware operator's
manual has the YAML for its class:
[displays](https://liken.sh/display/docs/guides/install/),
[audio outputs](https://liken.sh/audio/docs/guides/install/), and
[Bluetooth devices](https://liken.sh/bluetooth/docs/guides/install/).

The only class that the install creates is `media-render`, in
`capabilities.yaml`, and only the capabilities agent's own claim on
the render nodes uses it. To claim a GPU by what its driver can
decode, encode, or scale, a workload needs classes that you write.
[Claim a GPU by what it decodes](https://liken.sh/media/docs/guides/claim-a-gpu-by-capability/)
has the YAML.

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

The `-n` flag puts the `ServiceAccounts`, `Deployments`, `DaemonSet`,
and `Services` in `liken-system`, which every `liken` cluster has. The
CRDs, `DeviceClasses`, and `ClusterRoles` are cluster-scoped, so the
flag does not apply to them.

`api-authentication.yaml` holds a `RoleBinding` in `kube-system`, and
`kubectl` refuses an object in another namespace when you pass `-n
liken-system`. Apply that file without `-n`:

    kubectl apply -f https://liken.sh/media/deploy/api-authentication.yaml

For GitOps, point a `Kustomization` at the served URLs. `kustomize`
accepts a URL of raw YAML as a resource:

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
      - https://liken.sh/media/deploy/players-crd.yaml
      - https://liken.sh/media/deploy/plays-crd.yaml
      - https://liken.sh/media/deploy/remotes-crd.yaml
      - https://liken.sh/media/deploy/keymaps-crd.yaml
      - https://liken.sh/media/deploy/mediapreferences-crd.yaml
      - https://liken.sh/media/deploy/rbac.yaml
      - https://liken.sh/media/deploy/operator.yaml
      - https://liken.sh/media/deploy/api.yaml
      - https://liken.sh/media/deploy/api-authentication.yaml
      - https://liken.sh/media/deploy/bus.yaml
      - https://liken.sh/media/deploy/capabilities.yaml

The transformer sets `liken-system` only on objects that have no
namespace, so the `RoleBinding` in `api-authentication.yaml` stays in
`kube-system`. Do not use a top-level `namespace:` field. It moves the
`RoleBinding` to `liken-system`, where the `Role` it binds does not
exist, and then `media-api` cannot read the client certificate
authority that it checks callers against.

A clone works too: `kubectl apply -k media-operator/deploy/` from the
repository applies the same files through
[`deploy/kustomization.yaml`](https://liken.sh/media/deploy/kustomization.yaml).

## Running a development build

Each push to `main` that changes the operator publishes a development
build. Its version is the most recent release tag plus a suffix: for
example, `2026.09.03-007-dev-003-abcdef01` is commit `abcdef01`,
three commits after release `2026.09.03-007`. Every image of the
operator has that version, and `:latest` still points to the most
recent release.

A development build has no git tag, so pin the manifests to the
commit's full sha, and pin each image to the version:

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

Git needs the full sha to fetch a commit, and the version has only
the first eight characters. The summary of the CI run for the commit
lists the version.

The same build publishes the manifests as the OCI artifact
`oci://ghcr.io/liken-sh/media-operator-deploy:<version>`, with each
image set to that version. A Flux `OCIRepository` can pull that
artifact by version, with no sha.

A Flux `Kustomization` that applies the base must not set
`targetNamespace`. Flux sets that namespace on every object, as a
top-level `namespace:` field does, and the `RoleBinding` in
`api-authentication.yaml` must stay in `kube-system`.

## What the install runs

All of these run in `liken-system`:

* The `media-operator` `Deployment` watches the media resources and
  creates the claims and pods that they need. It reads its state from
  the API server and the playback pods' reports from the bus, so it
  keeps no state on a volume. Its only listener serves Prometheus
  metrics on port 9200. Only the copy that holds the `Lease` named
  `media-operator` in `liken-system` makes changes, so a second copy
  during a rollout waits and does nothing.
* The `media-api` `Deployment` serves HTTPS requests about a `Player`:
  the capture routes of
  [Record what a player is playing](https://liken.sh/media/docs/guides/record/) and the
  [API reference](https://liken.sh/media/docs/reference/api/). It runs the
  `media-operator-api` image, which contains `ffmpeg`, with one
  replica and the `Recreate` strategy, so two pods never run at once.
  The `Service` `media-api` serves it on port 443.
* The `bus` `Deployment` runs a [Mosquitto](https://mosquitto.org/)
  broker, with a `Service` at `bus.liken-system.svc:1883`. The broker
  has its own pod, so the operator can restart without losing
  messages, and a button press still reaches `mpv` while the operator
  is down.
* The `media-capabilities` `DaemonSet` is the capabilities agent, one
  pod on each node with a GPU. The pod takes a shareable claim on
  every render node of its node, asks each render node's VA-API driver
  what it supports, and publishes a `media.liken.sh` device for each
  GPU. It also registers the `media.liken.sh` DRA driver with the
  kubelet, which the kubelet needs before it can start a pod whose
  claim holds one of those devices.
  [Render node capabilities](https://liken.sh/media/docs/reference/capabilities/) describes
  the devices.

## Container resources

Every container that the operator creates has a cpu request, a memory
request, and a memory limit, and no cpu limit, because a throttled
decoder drops frames. The default requests let a playback pod and an
idle pod run together on a 1GB machine with a 1920x1080 screen, and
the default memory limits are high enough for a film on a 3840x2160
screen.

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
The operator reads it once, at start. The generator adds a hash of the
content to the `ConfigMap`'s name, so a changed value changes the
operator's `Deployment` and restarts the operator. The restarted
operator recreates the idle pod and each `Remote`'s pod with the new
values. A playback pod that is running keeps its values until its film
ends.

To change a value, copy `deploy/container-resources.yaml` into your
overlay, edit it, and merge it into the base's `ConfigMap` under the
key `resources.yaml`. This overlay raises the player's memory limit:

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

When your file leaves out a value, or the value is not a Kubernetes
quantity, the operator uses the default for that value and logs a line
at start:

    media.liken.sh: container resources: player: limits.memory is unset; using the default 1Gi

The operator also logs a line and ignores the value for a `limits.cpu`.
When a memory request is above its limit, the API server would refuse
the pod, so the operator logs a line and uses the defaults for both.

## Watch it start

    kubectl -n liken-system get pods

The pods of `media-operator`, `media-api`, and `bus` are `Running`, and
so is a `media-capabilities` pod on each node with a GPU. The
operator's log shows that it holds the `Lease`, and then the number of
plays and remotes it manages. client-go's leader election writes
lines of its own too:

    kubectl -n liken-system logs deploy/media-operator
    media.liken.sh: holding Lease liken-system/media-operator as media-operator-7d9f8b6c5d-x2k4p_1a2b3c4d
    media.liken.sh: operating 0 plays and 0 remotes over bus.liken-system.svc:1883

A copy that logs `waiting for Lease liken-system/media-operator` is a
second copy. It takes over when the holder releases the `Lease` at
shutdown, or, when the holder stops without releasing it, 30 seconds
after the holder's last renewal. A holder that cannot renew the
`Lease`, for example while the API server restarts, stops writing
once its last renewal is 10 seconds old, which is before a waiting
copy can take the `Lease`.

The capabilities agent logs a line for each GPU, with the driver's
name and every capability that the agent publishes:

    kubectl -n liken-system logs ds/media-capabilities
    capabilities: pci-0000-00-02-0 (/dev/dri/renderD128, Intel iHD driver for Intel(R) Gen Graphics - 25.2.3 ()): decodeH264=true decodeHEVCMain=true ...
    slice: created node-1-media.liken.sh with 1 devices

Next, declare your resources. The [reference](https://liken.sh/media/docs/reference/)
describes each one, and [the message bus](https://liken.sh/media/docs/reference/bus/)
describes every topic that the pods and your own programs share.

## Keep the capabilities pods off nodes with no GPU

The `DaemonSet` creates a pod on every node. On a node with no GPU,
the pod's claim matches no device, and the pod stays `Pending`. To
keep the pod off such a node, label the node `media.liken.sh/gpu:
none`. The `DaemonSet` in the base already has this node affinity, so
you need no patch:

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

`NotIn` also matches a node that has no such label
([set-based requirements](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#set-based-requirement)),
so the `DaemonSet` runs a pod on every node except the ones labeled
`none`. When a label changes, the
[`DaemonSet`](https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/)
controller deletes or creates the pod on that node to match.

Declare the label in the node's `Machine`, so it is recorded with the
rest of the machine's declaration:

    spec:
      nodeLabels:
        media.liken.sh/gpu: none

A `Machine` accepts a key in a `liken.sh` subdomain from `liken`
2026.09.28-002 on. An older release refuses it, so on an older release
use the `kubectl label` method below.

The `liken` machine operator applies the label to the running node,
and a machine that is demoted or installed again registers its new
`Node` with the label. To run the pod on that node again, remove the
key from `spec.nodeLabels`, and `liken` removes the label from the
node, even when you first set the label with `kubectl`.

You can also label the node with `kubectl`:

    kubectl label node node-1 media.liken.sh/gpu=none

`liken` removes only labels that a `Machine` declared, so this label
stays on the node across reboots. When the machine is demoted or
installed again, it registers a new `Node` without the label, and you
label it again. To run the pod on that node again, remove the label:

    kubectl label node node-1 media.liken.sh/gpu-

A patch of your own that sets a node affinity on this `DaemonSet`
replaces the base's list of terms, `none` requirement included, so copy
that requirement into each term of your patch.

## Read the player's full output

The playback pod runs `mpv` with `--quiet`, because `mpv` prints its
status line many times a second and each line goes to the pod log.
Warnings and errors still print. To read the full output of `mpv`, set
this environment variable on the operator:

```sh
kubectl set env deployment/media-operator MEDIA_PLAYER_VERBOSE=1
```

The variable removes `--quiet` and adds no other option. It takes
effect on the next `Play`, because only playback pods that the
operator creates after you set it read it. Read the log with
`kubectl logs <play>-playback -c player`. To turn the variable off:

```sh
kubectl set env deployment/media-operator MEDIA_PLAYER_VERBOSE-
```

## Remove the operator

Deleting a `Play` stops its run. Deleting a `Player` or a `Remote`
deletes its pods and claims through their `ownerReferences`. To remove
the operator itself:

    kubectl delete -n liken-system \
      -f https://liken.sh/media/deploy/rbac.yaml \
      -f https://liken.sh/media/deploy/operator.yaml \
      -f https://liken.sh/media/deploy/api.yaml \
      -f https://liken.sh/media/deploy/bus.yaml \
      -f https://liken.sh/media/deploy/capabilities.yaml
    kubectl delete -f https://liken.sh/media/deploy/api-authentication.yaml

Deleting a CRD deletes every resource of that kind. Delete the
`*-crd.yaml` files only when every `Player`, `Play`, `Remote`,
`Keymap`, and `MediaPreferences` in the cluster can be deleted with
them.
