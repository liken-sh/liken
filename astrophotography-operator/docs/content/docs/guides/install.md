---
title: Install the operator
weight: 10
description: "Install astrophotography-operator beside observatory-operator on a liken cluster, check that it leads, and declare a first Target. Use when a cluster has no astrophotography.liken.sh resources yet, when running a development build, or when removing the operator."
---

# Install the operator

This guide installs `astrophotography-operator` on a
[`liken`](https://liken.sh/docs/) cluster that runs
`observatory-operator`, checks that the operator runs and holds its
`Lease`, and declares a first `Target`. No telescope moves until you
reserve one for the operator, which
[Image a target](/docs/guides/image-a-target/) does.

You need:

* A `liken` cluster with `observatory-operator` installed.
  [Its install guide](https://liken.sh/observatory/docs/guides/install/)
  runs an example observatory of INDI simulators.
* `kubectl` with cluster-admin access, because the install creates
  two CRDs and a `ClusterRole`.

## Apply the manifests

The operator has no release yet, so you install a development build.
Every push to `main` that changes the operator publishes one. Its
version is the most recent release tag of the repository, plus a
suffix: `2026.10.04-004-dev-124-93f85ef4` is 124 commits past
release `2026.10.04-004`, at commit `93f85ef4`. The summary of the CI
run for that commit gives the version, and the
[package page](https://github.com/liken-sh/liken/pkgs/container/astrophotography-operator)
lists every version that exists.

A development build has no git tag, so the manifests pin to the
commit's full sha, and the image pins to the version. Write this
`kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - https://github.com/liken-sh/liken//astrophotography-operator/deploy?ref=<full 40-character sha>
images:
  - name: ghcr.io/liken-sh/astrophotography-operator
    newTag: <version>
```

A git fetch by sha needs all forty characters. The eight in the
version are not enough. Then apply it:

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

This site also serves the [manifests](/deploy/kustomization.yaml) as
raw YAML, if you want to read them first or write your own.

Check that the operator runs, and that it holds the `Lease`:

```sh
kubectl -n liken-system rollout status deployment/astrophotography-operator
kubectl -n liken-system get lease astrophotography-operator
```

The `Lease`'s holder is the operator's pod and a random suffix.

## Declare a target

The operator watches every namespace, and a target belongs in the
namespace of your observatory, beside the claims it names. This guide
uses `observatory`, the namespace of `observatory-operator`'s example:

```sh
kubectl apply -n observatory -f - <<'EOF'
apiVersion: astrophotography.liken.sh/v1alpha1
kind: Target
metadata:
  name: m42
spec:
  object: M42
  coordinates: {ra: "05:35:17.3", dec: "-05:23:28"}
  sequences:
    - {filter: Luminance, exposure: 10s, count: 3}
  archive:
    claimName: archive
EOF
```

Both kinds are in the category `astro` with `observatory-operator`'s
kinds, so one command lists the observatory and its targets:

```sh
kubectl get astro -n observatory
```

The API server fills in the defaults, such as `archive.format` and
`archive.promotion`, and refuses a field it cannot read. A right
ascension in decimal hours, such as `5.588`, is refused with a message
that gives the form. [Targets](/docs/concepts/targets/) explains each
field.

## Rollouts and failover

Only one copy of the operator acts at a time: the copy that holds the
`Lease` `astrophotography-operator` in `liken-system`. Every other
copy only reads the `Lease`, and opens no watch. A rollout starts the
new pod beside the old one, and the new pod waits for the `Lease`.
When the old pod stops, it closes its watches, waits until its last
write to the API server returns, and releases the `Lease`, and the new
pod takes it within about 11 seconds. When the leading pod cannot
renew the `Lease`, a waiting copy takes it 30 to 41 seconds after the
last renewal.

## Collect metrics

The operator serves Prometheus metrics on port `9200`. If you run the
prometheus-operator, add the monitoring component beside the base in
your `kustomization.yaml`. It adds a `PodMonitor` in `liken-system`
and a Grafana dashboard in the `monitoring` namespace:

```yaml
resources:
  - https://github.com/liken-sh/liken//astrophotography-operator/deploy?ref=<full 40-character sha>
components:
  - https://github.com/liken-sh/liken//astrophotography-operator/deploy/monitoring?ref=<full 40-character sha>
```

[Metrics](/docs/reference/metrics/) lists every series.

## Remove the operator

Delete the base:

```sh
kubectl delete -k .
```

This deletes the CRDs, and with them every `Target` and `Solver` in
the cluster. `observatory-operator` and its resources stay.
