---
name: gitops
description: "Run a liken fleet from a git repository with the flux feature: lay out the repository, register the deploy key, watch the first sync, and work by commit. Use when a cluster's machines and workloads should converge to git, or when rotating the deploy key or turning the feature off."
---

This skill is the guide at https://liken.sh/docs/guides/gitops/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Run the fleet from git

The `flux` feature connects a cluster to a git repository. The cluster
runs [Flux](https://fluxcd.io), which syncs the repository and applies
the manifests in it. The repository then declares everything: the
Cluster document, the Machine documents, and your workloads. You make
each change with a commit, and the fleet converges to the repository.

You need:

* A cluster, either running or ready to install. [Install a
  cluster](https://liken.sh/docs/guides/install/) has those steps.
* A private, empty git repository at a forge you can reach over SSH.
* The [Flux CLI](https://fluxcd.io/flux/installation/) on your
  workstation, for one command in step 1.

## 1. Lay out the repository

The cluster syncs one path of the repository. The `path` parameter
selects the path, and the default is the repository root. Use this
layout:

    flux-system/
      gotk-components.yaml    the Flux engine
    liken/
      cluster.yaml            the Cluster document
      node-1.yaml             one file per Machine

Export the engine manifest with the Flux CLI:

    flux install --export \
      --components=source-controller,kustomize-controller \
      > flux-system/gotk-components.yaml

The engine manifest is in the repository so that you control the
engine through git. `liken` installs its own pinned copy of these two
controllers only so that the first sync can run. After that, the
cluster runs the engine from your repository. To upgrade Flux or to
add a controller, commit a change to this file.

Copy your `cluster.yaml` and machine manifests into `liken/`. These
are the files `liken new` wrote. Then add this annotation to the
Cluster document and to every Machine document:

    metadata:
      annotations:
        kustomize.toolkit.fluxcd.io/prune: disabled

Flux deletes objects that are no longer in the synced path. Without
the annotation, a commit that removes the Cluster or a Machine
document also deletes it from the live cluster. With the annotation,
Flux leaves the object, and you delete it yourself when you want it
gone.

Do not add `GitRepository` or `Kustomization` objects to the
repository. `liken` creates these two sync objects from the feature's
parameters, and a copy in the repository conflicts with them.

## 2. Declare the feature

Collect the forge's SSH host keys, and compare them with the keys that
the forge publishes:

    ssh-keyscan github.com

Then declare the feature on the Cluster. On a running cluster, use
`kubectl edit cluster`. On a cluster you did not install yet, put the
same block in your `cluster.yaml` before you build the stick. The
fleet then syncs from the first boot.

    spec:
      features:
        flux:
          repository: ssh://git@github.com/you/fleet.git
          knownHosts: |
            github.com ssh-ed25519 AAAAC3NzaC1lZDI1...
            github.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNo...
            github.com ssh-rsa AAAAB3NzaC1yc2EAAA...

`repository` is required. `branch` defaults to `main`, and `path`
defaults to the repository root. `knownHosts` lists the forge's host
keys, one per line, so the first clone can verify the forge. Host
keys are public, so they can go in the spec.
The [Cluster reference](https://liken.sh/docs/reference/cluster/#spec--features)
describes each parameter.

On a running cluster, k3s restarts on each machine, one machine at a
time, to apply this edit. The machines do not reboot, and their pods
keep running.

## 3. Register the deploy key

The cluster creates its own SSH deploy key and keeps the private key
inside the cluster. Read the public key:

    kubectl get cluster -o jsonpath='{.items[0].status.flux.publicKey}{"\n"}'

Register this value at the forge as a deploy key for the repository,
and give it write access. On GitHub, the setting is in the
repository's Settings, then Deploy keys. The sync starts when the
forge accepts the key.
[`status.flux`](https://liken.sh/docs/reference/cluster/#statusflux) describes how
the key is created, rotated, and removed.

## 4. Watch the first sync

    kubectl --namespace flux-system get gitrepositories,kustomizations

When both objects show Ready, the fleet follows the repository. The
first sync also replaces the engine that `liken` installed with the
copy from your repository.

## 5. Work by commit

From now on, change the fleet with a commit. Add a workload manifest
under the synced path, and the cluster runs it. Edit a feature or a
Machine's disks in `liken/`, and the fleet applies the change as it
does for a live edit. To [upgrade the fleet](https://liken.sh/docs/guides/upgrade/), commit the
new catalog entry and `spec.version` in `liken/cluster.yaml`.

## Rules for safe operation

* Never commit the sync objects. `liken` creates and manages
  `GitRepository` and `Kustomization`.
* Keep the `prune: disabled` annotation on the Cluster and Machine
  documents.
* Watch memory use. The engine manifest in your repository sets
  which controllers run. `source-controller` and `kustomize-controller`
  fit on a 1 GB machine with its workloads. Each controller that you
  add uses memory that the workloads need, so add them one at a time
  and run `kubectl top nodes` after each one.
* Commit each live repair. The API server records you as an owner of
  each field that you change with `kubectl`, and a Flux sync cannot
  delete a field that you own. After a repair with `kubectl`, commit
  the same state to the repository. If a later commit removes a field
  that you changed live, remove that field live too.

## Rotate the key, turn the feature off, or repair the engine

To rotate the deploy key, delete the `Secret`. The cluster creates a
new key pair within seconds, and you register the new public key at
the forge:

    kubectl --namespace flux-system delete secret flux-system

To turn the feature off, remove `flux` from `spec.features`. The sync
stops, and the cluster removes the engine, its namespace, and the
deploy key. The workloads that the repository deployed keep running.
To turn the feature on again, declare it again and register the new
key that the cluster creates. `liken` removes only a Flux that it
installed, as the next section describes.

If someone deletes the engine while the feature is on, the cluster
installs it again within about a minute, and the next sync replaces
it with the copy from the repository.

## What `liken` owns

Declaring the feature puts this annotation on the `flux-system`
namespace:

    liken.sh/feature: flux

The annotation marks this Flux installation as `liken`'s. When the
Cluster does not declare the feature, `liken` reads the annotation
before it deletes anything, and deletes nothing unless the namespace
has it. So a Flux that you installed yourself keeps running. In that
case the Cluster's `FluxTeardown` condition is `False` with the
reason `NotPlantedByLiken`, and its message gives the command that
hands the installation to `liken`:

    kubectl describe cluster

To hand an existing Flux installation to `liken`, declare the
feature. The namespace does not have to be new. `liken` applies its
own manifest over whatever is there and writes the annotation. From
then on, turning the feature off removes the engine, its namespace,
and the deploy key, the same as for an installation that `liken`
created. Writing the annotation by hand has the same effect:

    kubectl annotate namespace flux-system liken.sh/feature=flux
