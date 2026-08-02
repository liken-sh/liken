---
title: Run the fleet from git
weight: 60
---

# Run the fleet from git

The `flux` feature connects a cluster to a git repository. The cluster
runs [Flux](https://fluxcd.io), syncs the repository, and applies what
the repository holds. The repository then declares everything: the
Cluster document, the Machine documents, and your workloads. You make
each change with a commit, and the fleet converges to the repository.

You need:

* A cluster, either running or ready to install. [Install a
  cluster](/docs/guides/install/) has those steps.
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

The repository holds the engine manifest, because the repository
controls the engine. liken installs a pinned copy of these two
controllers one time, and only to make the first sync possible. After
the first sync, the cluster runs the engine from your repository. To
upgrade Flux, commit a change to this file. To add a controller,
commit a change to the same file.

Copy your `cluster.yaml` and machine manifests into `liken/`. These
are the files `liken new` wrote. Then add this annotation to the
Cluster document and to every Machine document:

    metadata:
      annotations:
        kustomize.toolkit.fluxcd.io/prune: disabled

The annotation prevents Flux from deleting the fleet's documents. Flux
deletes the objects that are no longer in the synced path. Without the
annotation, a commit that removes these documents also removes the
fleet's declaration from the live cluster. With the annotation, Flux
keeps the marked documents, and you must remove the fleet
deliberately.

Do not add `GitRepository` or `Kustomization` objects to the
repository. liken renders these two sync objects from the feature's
parameters. A copy in the repository conflicts with the rendered
object.

## 2. Declare the feature

Collect the forge's SSH host keys, and compare them with the keys the
forge publishes:

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

`repository` is required. The default for `branch` is `main`, and the
default for `path` is the repository root. `knownHosts` holds the
forge's host keys, one key per line. The keys are public material, so
they belong in the spec. They let the first clone verify the forge.
The [Cluster reference](/docs/reference/cluster/#spec--features)
describes each parameter.

On a running cluster, k3s restarts in place on each machine, one
machine at a time, to apply this edit. The machines and their pods
stay up.

## 3. Register the deploy key

The cluster creates its own SSH deploy key. The private half stays in
the cluster. Read the public half:

    kubectl get cluster -o jsonpath='{.items[0].status.flux.publicKey}{"\n"}'

Register this value at the forge as a deploy key for the repository,
and give it write access. On GitHub, the setting is in the
repository's Settings, then Deploy keys. The sync starts when the
forge accepts the key.
[`status.flux`](/docs/reference/cluster/#statusflux) describes the
lifecycle of the key.

## 4. Watch the first sync

    kubectl --namespace flux-system get gitrepositories,kustomizations

When both objects show Ready, the repository controls the fleet. The
first sync also replaces the installed engine with the copy from your
repository.

## 5. Work by commit

From now on, change the fleet with a commit. Add a workload manifest
under the synced path, and the cluster runs it. Edit a feature or a
Machine's disks in `liken/`, and the fleet converges as it does for a
live edit. To [upgrade the fleet](/docs/guides/upgrade/), commit the
new catalog entry and `spec.version` in `liken/cluster.yaml`.

## Rules for safe operation

* Never commit the sync objects. liken owns `GitRepository` and
  `Kustomization`.
* Keep the `prune: disabled` annotation on the Cluster and Machine
  documents.
* Monitor the memory. The repository sets how many controllers run.
  The `source-controller` and the `kustomize-controller` fit on a 1 GB
  machine with its workloads. Each controller you add uses memory that the
  workloads need. Add components one at a time, and look at
  `kubectl top nodes` after each one.
* A live edit changes field ownership. The API server records you as
  an owner of each field you change with `kubectl`, and git cannot
  delete a field that you own. After each manual repair, commit the
  same state to the repository. If a later commit must remove a field
  that you changed live, also remove that field live.

## Rotate, retract, recover

To rotate the deploy key, delete the Secret. The cluster creates a new
key pair in seconds, and you register the new public half at the
forge:

    kubectl --namespace flux-system delete secret flux-system

To turn the feature off, remove `flux` from `spec.features`. The sync
stops, and the cluster removes the engine, its namespace, and the
deploy key. The workloads that the repository deployed continue to
run. Retraction stops the sync. It does not remove the workloads. To
turn the feature on again, declare it again and register the new key
it creates.

Retraction removes only the Flux that `liken` installed. The next
section says how `liken` separates the two cases.

If someone deletes the engine by accident, the cluster installs it
again in about a minute, and the next sync restores the copy from the
repository.

## What liken owns

Declaring the feature puts this annotation on the `flux-system`
namespace:

    liken.sh/feature: flux

The annotation is the record that this installation is `liken`'s. The
teardown reads it before it deletes anything, and removes only what
carries it.

A cluster that never declares the feature never gets the annotation.
`liken` seeds nothing there, so a Flux that was running before `liken`
arrived keeps running, and retracting a feature the document never
declared deletes nothing. The Cluster says so:

    kubectl describe cluster

The `FluxTeardown` condition names what the teardown declined. A
cluster that `liken` founded before this record existed reports the
same thing until its namespace carries the annotation.

Declaring the feature is how you hand an existing installation to
`liken`. The namespace does not have to be new: `liken` applies its
own copy over whatever is there, and the annotation lands with it.
From that point retraction removes the engine, its namespace, and the
deploy key, exactly as it does for an installation `liken` made.
Writing the annotation by hand does the same thing:

    kubectl annotate namespace flux-system liken.sh/feature=flux
