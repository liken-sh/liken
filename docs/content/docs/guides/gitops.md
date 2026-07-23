---
title: Run the fleet from git
weight: 60
---

# Run the fleet from git

The `flux` feature connects a cluster to a git repository. The
cluster runs [Flux](https://fluxcd.io), syncs the repository, and
applies what the repository holds. The repository then declares
everything: the Cluster document, the Machine documents, and your
workloads. A change is a commit, and the fleet converges to it.

You need:

* A cluster, either running or about to be installed. [Install a
  cluster](/docs/guides/install/) has those steps.
* A private, empty git repository at a forge you reach over SSH.
* The [Flux CLI](https://fluxcd.io/flux/installation/) on your
  workstation, for one command in step 1.

## 1. Lay out the repository

The cluster syncs one path of the repository. The `path` parameter
selects it, and it defaults to the repository root. Lay the path out
like this:

    flux-system/
      gotk-components.yaml    the Flux engine
    liken/
      cluster.yaml            the Cluster document
      node-1.yaml             one file per Machine

Export the engine manifest with the Flux CLI:

    flux install --export \
      --components=source-controller,kustomize-controller \
      > flux-system/gotk-components.yaml

The repository carries the engine because the repository owns it.
liken plants a pinned copy of these two controllers once, and only
to reach the first sync. From then on, the engine in your repository
is the engine your cluster runs. A Flux upgrade is a commit to this
file, and an added controller is a commit too.

Copy your `cluster.yaml` and machine manifests into `liken/`. These
are the files `liken new` wrote. Then add this annotation to the
Cluster document and to every Machine document:

    metadata:
      annotations:
        kustomize.toolkit.fluxcd.io/prune: disabled

The annotation protects the fleet from its own repository. Flux
deletes objects that leave the synced path. Without the annotation,
a commit that drops these documents deletes the fleet's declaration
from the live cluster. With it, Flux leaves the marked documents in
place, and removing the fleet stays a deliberate act.

Do not add `GitRepository` or `Kustomization` objects to the
repository. liken renders these two sync objects from the feature's
parameters. A copy in the repository would fight the rendered one.

## 2. Declare the feature

Collect the forge's SSH host keys, and check them against the keys
the forge publishes:

    ssh-keyscan github.com

Then declare the feature on the Cluster. On a running cluster, use
`kubectl edit cluster`. On a cluster you have not installed yet, put
the same block in your `cluster.yaml` before you build the stick,
and the fleet syncs from first boot.

    spec:
      features:
        flux:
          repository: ssh://git@github.com/you/fleet.git
          knownHosts: |
            github.com ssh-ed25519 AAAAC3NzaC1lZDI1...
            github.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNo...
            github.com ssh-rsa AAAAB3NzaC1yc2EAAA...

`repository` is required. `branch` defaults to `main`, and `path`
defaults to the repository root. `knownHosts` holds the forge's host
keys, one per line. The keys are public material, so they belong in
the spec: they let the first clone verify the forge. The
[Cluster reference](/docs/reference/cluster/#spec) describes each
parameter.

On a running cluster, this edit converges by restarting k3s in
place on each machine, one machine at a time. The machines and
their pods stay up.

## 3. Register the deploy key

The cluster mints its own SSH deploy key. The private half lives in
the cluster and never leaves it. Read the public half:

    kubectl get cluster -o jsonpath='{.items[0].status.flux.publicKey}{"\n"}'

Register this value at the forge as a deploy key for the
repository, and allow write access. On GitHub, the setting is under
the repository's Settings, then Deploy keys. The sync starts when
the forge accepts the key.
[`status.flux`](/docs/reference/cluster/#statusflux) describes the
key's lifecycle.

## 4. Watch the first sync

    kubectl --namespace flux-system get gitrepositories,kustomizations

When both objects show Ready, the repository is in charge. The
first sync also replaces the planted engine with your repository's
copy.

## 5. Work by commit

From now on, change the fleet by commit. Add a workload manifest
under the synced path, and the cluster runs it. Edit a feature or a
Machine's disks in `liken/`, and the fleet converges the same way a
live edit would. To [upgrade the fleet](/docs/guides/upgrade/),
commit the new catalog entry and `spec.version` in
`liken/cluster.yaml`.

## The rules that keep the loop safe

* Never commit the sync objects. liken owns `GitRepository` and
  `Kustomization`.
* Keep the `prune: disabled` annotation on the Cluster and Machine
  documents.
* Watch the memory. The repository decides how many controllers
  run. The two floor controllers fit a 1 GB machine beside its
  workloads; each controller you add costs memory the workloads
  need. Add components one at a time, and watch `kubectl top nodes`
  after each one.
* A live edit leaves fingerprints. The API server records you as an
  owner of every field you touch with `kubectl`, and git cannot
  delete a field you own. End every rescue by committing the same
  state to the repository. If a later commit must remove a field
  you touched live, remove it live as well.

## Rotate, retract, recover

To rotate the deploy key, delete the Secret. The cluster mints a
fresh pair within seconds, and you register the new public half at
the forge:

    kubectl --namespace flux-system delete secret flux-system

To turn the feature off, remove `flux` from `spec.features`. The
sync stops, and the engine, its namespace, and the deploy key are
removed. What the repository deployed stays running: retraction
stops the sync, it does not undeploy. To turn the feature back on,
declare it again and register the fresh key it mints.

If someone deletes the engine by accident, the cluster replants it
within seconds, and the next sync restores the repository's copy.
