---
title: Install the operator
weight: 10
description: "Install astrophotography-operator beside observatory-operator on a liken cluster, check that it runs and holds its Lease, and collect its metrics. Use when a cluster has no astrophotography.liken.sh resources yet, when running a development build, or when removing the operator."
---

# Install the operator

This guide installs `astrophotography-operator` on a
[`liken`](https://liken.sh/docs/) cluster that runs
`observatory-operator`, and checks that the operator runs and holds
its `Lease`. The operator moves no telescope until you reserve one for
it, as [Image a target](/docs/guides/image-a-target/) shows.

You need:

* A `liken` cluster with `observatory-operator` installed.
  [Its install guide](https://liken.sh/observatory/docs/guides/install/)
  runs an example observatory of INDI simulators.
* `kubectl` with cluster-admin access, because the install creates
  two CRDs and a `ClusterRole`.

## Apply the manifests

Pin the manifests and the image to one release. The
[GitHub releases](https://github.com/liken-sh/liken/releases) list
each tag and the version of every component in it, and a component's
version is the tag that last released it. Write this
`kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - https://github.com/liken-sh/liken//astrophotography-operator/deploy?ref=<version>
images:
  - name: ghcr.io/liken-sh/astrophotography-operator
    newTag: <version>
```

Then apply it:

```sh
kubectl apply -k .
```

The base creates:

* the CRDs of the `astrophotography.liken.sh` group, `Target` and
  `Solver`
* a `ServiceAccount`, a `ClusterRole`, and a `ClusterRoleBinding`
* a `Role` and a `RoleBinding` that let the operator hold its `Lease`,
  `astrophotography-operator` in `liken-system`
* the operator, a `Deployment` with one replica in `liken-system`, the
  namespace of every `liken` operator

Later, the operator creates a `Deployment` and a `Service` for each
`Solver` and for each reservation it holds, in that resource's
namespace. Their pods run `ghcr.io/liken-sh/astrophotography-operator-worker`
with the same tag as the operator's image, so the `newTag` above sets
both images. If you pin the operator's image by digest, which has no
tag, set the environment variable `ASTROPHOTOGRAPHY_WORKER_IMAGE` on
the operator's `Deployment` to the full worker image reference.

This site also serves the [manifests](/deploy/kustomization.yaml) as
raw YAML, so you can read them first or write your own.

Check that the operator runs, and that it holds the `Lease`:

```sh
kubectl -n liken-system rollout status deployment/astrophotography-operator
kubectl -n liken-system get lease astrophotography-operator
```

The `HOLDER` column starts with the operator's pod name.

Check that the API server serves both kinds. They are in the category
`astro`, with `observatory-operator`'s kinds, so one command lists the
observatory and its targets:

```sh
kubectl get crd targets.astrophotography.liken.sh solvers.astrophotography.liken.sh
kubectl get astro -A
```

The operator watches every namespace. Next,
[declare a target](/docs/guides/declare-a-target/) in the namespace of
your observatory.

## Rollouts and failover

Only the operator pod that holds the `Lease`
`astrophotography-operator` in `liken-system` does any work. Every
other operator pod waits for the `Lease` and opens no watch. During a rollout,
the new pod starts beside the old one and waits. When the old pod
stops, it waits for its last write to the API server to return and
then releases the `Lease`, and the new pod takes it within about 11
seconds. When the leader stops renewing the `Lease` without releasing
it, for example because its node fails, a waiting pod takes it 30 to
41 seconds after the last renewal.

## Collect metrics

The operator serves Prometheus metrics on port `9200`. If you run the
prometheus-operator, add the `deploy/monitoring` component to your
`kustomization.yaml`. It adds a `PodMonitor` in `liken-system` and a
Grafana dashboard in the `monitoring` namespace:

```yaml
resources:
  - https://github.com/liken-sh/liken//astrophotography-operator/deploy?ref=<version>
components:
  - https://github.com/liken-sh/liken//astrophotography-operator/deploy/monitoring?ref=<version>
```

[Metrics](/docs/reference/metrics/) lists every series.

## Run a development build

Each push to `main` that changes the operator publishes a development
build. Its version is the most recent release tag plus a suffix:
`2026.10.13-003-dev-012-93f85ef4` is 12 commits past release
`2026.10.13-003`, at commit `93f85ef4`. The summary of the CI run for
that commit gives the version, and `:latest` still names the most
recent release.

A development build has no git tag, so the manifests pin to the
commit's full sha, and the image pins to the version:

```yaml
resources:
  - https://github.com/liken-sh/liken//astrophotography-operator/deploy?ref=<full 40-character sha>
images:
  - name: ghcr.io/liken-sh/astrophotography-operator
    newTag: <version>
```

Use the full sha, because git cannot fetch a commit by the eight
characters in the version.

The same build publishes the manifests as the OCI artifact
`oci://ghcr.io/liken-sh/astrophotography-operator-deploy:<version>`,
with the image already set to that version. A Flux `OCIRepository` can
pull that artifact by its version, with no sha.

## Remove the operator

Delete the targets first, while the operator still runs. Each target
has a finalizer that only the operator removes, after it has moved the
target's pending frames to the archive:

```sh
kubectl delete targets --all --all-namespaces --wait=false
kubectl get targets --all-namespaces
```

When the second command lists no target, delete the base:

```sh
kubectl delete -k .
```

This deletes the CRDs, and with them every `Solver` in the cluster.
If a `Target` still has its finalizer, the `Target` CRD is not deleted
until you remove the finalizer
([A target does not delete](/docs/guides/troubleshoot/#a-target-does-not-delete)).
`observatory-operator` and its resources stay.
