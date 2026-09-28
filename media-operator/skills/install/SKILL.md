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
`MediaPreferences`.

You need:

* A `liken` cluster.
* The hardware operators for the devices your players will select:
  the [`display-operator`](https://liken.sh/display/) for a screen,
  the [`audio-operator`](https://liken.sh/audio/) for sound, and the
  [`bluetooth-operator`](https://liken.sh/bluetooth/) for controllers
  and Bluetooth speakers. Install the ones your equipment has; a
  `Player` can only select what an installed operator publishes.
* `kubectl` with cluster-admin access, because the install creates
  the CRDs and a `ClusterRole`.

## The device classes are yours

The install manifests define no `DeviceClass` objects. The operator
claims no devices for itself. The classes a `Player` names are the
cluster owner's vocabulary, the same classes a hand-written
`ResourceClaim` would use. Each hardware operator's manual gives the
YAML for its class:
[displays](https://liken.sh/display/docs/guides/install/),
[audio outputs](https://liken.sh/audio/docs/guides/install/), and
[Bluetooth devices](https://liken.sh/bluetooth/docs/guides/install/).

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
      -f https://liken.sh/media/deploy/bus.yaml

The `-n` flag places the `ServiceAccount`, the two `Deployments`,
and the `Service` in `liken-system`, the namespace every `liken`
cluster has. The CRDs and the `ClusterRole` are cluster-scoped, so
the flag does not apply to them.

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
      - https://liken.sh/media/deploy/bus.yaml

A clone works too: `kubectl apply -k deploy/` from the repository
applies the same files through
[`deploy/kustomization.yaml`](https://liken.sh/media/deploy/kustomization.yaml).

## Running a development build

Every push to the operator's main branch publishes a development
build. Its version is the most recent release plus a suffix:
`2026.09.03-007-dev-003-abcdef01` is three commits past release
`2026.09.03-007`, at commit `abcdef01`. Every image the repository
builds has the same version, and `:latest` still names the
most recent release.

A development build has no git tag, so the manifests pin to the
commit's full sha, and the image pins to the version:

```yaml
resources:
  - https://github.com/liken-sh/media-operator//deploy?ref=<full 40-character sha>
images:
  - name: ghcr.io/liken-sh/media-operator
    newTag: 2026.09.03-007-dev-003-abcdef01
```

A git fetch by sha needs all forty characters; the eight in the
version are not enough. The CI run for that commit prints both
lines in its summary.

## What the install runs

The install runs two `Deployments` in `liken-system`, and they are
separate on purpose:

* `media-operator` watches the five resources and reconciles them
  into claims and pods. It keeps no state on a volume and serves no
  HTTP. On
  every pass it re-derives everything from the API server, and it
  reads each playback pod's report from the bus. Only the copy that
  holds the `Lease` named `media-operator` in `liken-system`
  reconciles, so a second copy from a rollout or a larger replica
  count waits and changes nothing.
* `bus` is one [Mosquitto](https://mosquitto.org/) broker, with a
  `Service` at `bus.liken-system.svc:1883`. The broker is not inside
  the operator's pod, so the operator restarts without dropping a
  message, and a button press reaches `mpv` while the operator is
  down.

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
[`deploy/container-resources.yaml`](https://github.com/liken-sh/media-operator/blob/main/deploy/container-resources.yaml).
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
      - https://github.com/liken-sh/media-operator//deploy?ref=<tag>
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

Both pods report `Running`. The operator's log names the `Lease` it
holds, and then counts what it found. client-go's leader election
writes lines of its own beside these:

    kubectl -n liken-system logs deploy/media-operator
    media.liken.sh: holding lease liken-system/media-operator as media-operator-7d9f8b6c5d-x2k4p_1a2b3c4d
    media.liken.sh: operating 0 plays and 0 remotes over bus.liken-system.svc:1883

A copy that logs `waiting for lease liken-system/media-operator`
instead is a second copy. It takes over within about 11 seconds when
the holder shuts down, and 30 to 41 seconds after the holder's last
renewal when the holder stops without a shutdown.

From here, the work is declaring resources. The
[reference](https://liken.sh/media/docs/reference/) describes each one, and
[the message bus](https://liken.sh/media/docs/reference/bus/) describes every topic the
pods and your own programs share.

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
      -f https://liken.sh/media/deploy/bus.yaml

**Deleting a CRD deletes every resource of that kind.** Delete the
five `*-crd.yaml` files only when every player, play, remote,
keymap, and preference in the cluster can be deleted with them.
