---
title: Install the operator
weight: 10
---

# Install the operator

This guide installs `media-operator` on a
[`liken`](https://liken.sh/docs/) cluster. At the end, the operator
and its message bus run in `liken-system`, and the cluster accepts
the five resources: `Player`, `Play`, `Remote`, `Keymap`, and
`MediaPreferences`.

You need:

* A `liken` cluster.
* The hardware operators for the devices your players will select:
  the [`display-operator`](https://display.liken.sh) for a screen,
  the [`audio-operator`](https://audio.liken.sh) for sound, and the
  [`bluetooth-operator`](https://bluetooth.liken.sh) for controllers
  and Bluetooth speakers. Install the ones your equipment has; a
  `Player` can only select what an installed operator publishes.
* `kubectl` with cluster-admin access, because the install creates
  the CRDs and a `ClusterRole`.

## The device classes are yours

The base ships no `DeviceClass`. The operator claims no devices for
itself, and the classes a `Player` names are the cluster owner's
vocabulary, the same classes a hand-written `ResourceClaim` would
use. Each hardware operator's manual gives the YAML for its class:
[displays](https://display.liken.sh/docs/guides/install/),
[audio outputs](https://audio.liken.sh/docs/guides/install/), and
[Bluetooth devices](https://bluetooth.liken.sh/docs/guides/install/).

## Apply the manifests

This site serves the repository's
[`deploy/`](/deploy/kustomization.yaml) directory as raw YAML, so
the install needs no clone:

    kubectl apply -n liken-system \
      -f https://media.liken.sh/deploy/players-crd.yaml \
      -f https://media.liken.sh/deploy/plays-crd.yaml \
      -f https://media.liken.sh/deploy/remotes-crd.yaml \
      -f https://media.liken.sh/deploy/keymaps-crd.yaml \
      -f https://media.liken.sh/deploy/mediapreferences-crd.yaml \
      -f https://media.liken.sh/deploy/rbac.yaml \
      -f https://media.liken.sh/deploy/operator.yaml \
      -f https://media.liken.sh/deploy/bus.yaml

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
      - https://media.liken.sh/deploy/players-crd.yaml
      - https://media.liken.sh/deploy/plays-crd.yaml
      - https://media.liken.sh/deploy/remotes-crd.yaml
      - https://media.liken.sh/deploy/keymaps-crd.yaml
      - https://media.liken.sh/deploy/mediapreferences-crd.yaml
      - https://media.liken.sh/deploy/rbac.yaml
      - https://media.liken.sh/deploy/operator.yaml
      - https://media.liken.sh/deploy/bus.yaml

A clone works too: `kubectl apply -k deploy/` from the repository
applies the same files through
[`deploy/kustomization.yaml`](/deploy/kustomization.yaml).

## What the install runs

The install runs two `Deployments` in `liken-system`, and they are
separate on purpose:

* `media-operator` watches the five resources and reconciles them
  into claims and pods. It holds no volume and serves no HTTP; on
  every pass it re-derives everything from the API server, and it
  reads each playback pod's report from the bus.
* `bus` is one [Mosquitto](https://mosquitto.org/) broker, with a
  `Service` at `bus.liken-system.svc:1883`. The broker is not inside
  the operator's pod, so the operator restarts without dropping a
  message, and a button press reaches `mpv` while the operator is
  down.

## Watch it start

    kubectl -n liken-system get pods

Both pods report `Running`. The operator's first log line counts
what it found:

    kubectl -n liken-system logs deploy/media-operator
    media.liken.sh: operating 0 plays and 0 remotes over bus.liken-system.svc:1883

From here, the work is declaring resources. The
[reference](/docs/reference/) describes each one, and
[the message bus](/docs/reference/bus/) describes every topic the
pods and your own programs share.

## Remove the operator

Deleting a `Play` stops its run, and deleting a `Player` or a
`Remote` removes its standing pods and claims, through the
`ownerReference` every one of them carries. To remove the operator
itself:

    kubectl delete -n liken-system \
      -f https://media.liken.sh/deploy/rbac.yaml \
      -f https://media.liken.sh/deploy/operator.yaml \
      -f https://media.liken.sh/deploy/bus.yaml

**Deleting a CRD deletes every resource of that kind.** Delete the
five `*-crd.yaml` files only when every player, play, remote,
keymap, and preference in the cluster can be deleted with them.
