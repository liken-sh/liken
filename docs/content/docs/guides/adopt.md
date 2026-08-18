---
title: Adopt an existing k3s cluster
weight: 20
---

# Adopt an existing k3s cluster

Adoption joins `liken` machines to a k3s cluster that `liken` did not
create. You can replace the cluster's machines one at a time while the
cluster continues to serve. The procedure does not export or restore
cluster state. Each new member receives the data through usual
replication.

Adoption works with any k3s cluster that uses embedded etcd, on any
operating system.

Read this behavior before you start. A `liken` server disables the
bundled k3s components (traefik, servicelb, metrics-server), unless
your `cluster.yaml` declares them as features. The change applies to
the whole cluster when the first `liken` server joins. If your
workloads need a bundled component, declare its feature in your
`cluster.yaml`, or start a replacement before that first join.

## 1. Harvest the identity

On any server of the existing cluster, as root:

    cd /var/lib/rancher/k3s/server
    tar czf /tmp/identity.tgz token \
        tls/server-ca.{crt,key} \
        tls/client-ca.{crt,key} \
        tls/request-header-ca.{crt,key} \
        tls/service.key \
        tls/etcd/server-ca.{crt,key} \
        tls/etcd/peer-ca.{crt,key}

Copy the archive to your workstation and unpack it into a private
directory, for example `harvest/`. The archive contains only the
certificate authorities and the join token. It does not contain the
server's leaf certificates, because each server signs its own
certificates with the shared roots.

## 2. Arrange the identity

    ./liken new mycluster
    ./liken adopt harvest mycluster/identity

[`liken adopt`](/docs/reference/cli/#liken-adopt) puts the harvested
files into the identity directory in the same arrangement as
[`liken mint`](/docs/reference/cli/#liken-mint). It refuses an
incomplete harvest, and it makes sure that the token agrees with the
harvested certificate authority. After this step, the later steps are
the same for a minted identity and an adopted identity.

## 3. Declare the adoption

Edit `mycluster/cluster.yaml`:

* Set [`spec.origin`](/docs/reference/cluster/#spec--origin) to
  `Adopted`.
* Set `spec.endpoint` to the existing cluster's join URL.

The datastore of an adopted cluster already exists. Each `liken` leader
joins it through the endpoint, and no `liken` machine initializes a new
datastore. A second datastore next to a live one divides the cluster
into two clusters.

### The document claims the whole cluster

[`spec.features`](/docs/reference/cluster/#spec--features) is an
opt-in list, and `liken` reads it as a statement
about the cluster, not only about the machines you install. A feature
that the document does not name is a feature that the cluster
retracts, and a retracted feature is one `liken` tears down.

The `flux` feature shows what this means for an adopted cluster. If
the cluster already runs Flux, and your `cluster.yaml` does not
declare `flux`, that omission reads as a retraction.

`liken` does not delete that Flux. It removes a Flux installation only
when the `flux-system` namespace has a `liken.sh/feature=flux`
annotation, and that annotation arrives only when the document
declares the feature. A document that never declared `flux` never put
it there, so the teardown deletes nothing and the Cluster reports the
refusal:

    kubectl describe cluster

The `FluxTeardown` condition names what the teardown declined, and the
one command that gives the installation to `liken`. Decide before you
install the first machine:

* To keep the existing Flux and run it yourself, leave `flux` out of
  `spec.features`. The sync keeps running. The condition stays, and it
  is the report that `liken` manages none of it.
* To run the fleet from git with `liken`, declare the `flux` feature.
  [Run the fleet from git](/docs/guides/gitops/) has the steps, and
  its last section covers an installation that already exists.

## 4. Install the `liken` machines

Build the stick and install each machine as in
[Install a cluster](/docs/guides/install/). Start with the first
leader. Each machine joins the existing cluster directly. The existing
machines continue to serve during the procedure.

`liken` machines have a `liken.sh/machine=true` node label, and the OS
workloads schedule only onto nodes with that label. Thus the foreign
nodes get no changes.

## 5. Rotate the old servers out

Remove the foreign servers one at a time:

    kubectl delete node <old-server>

For a k3s server, the deletion of the node also removes the etcd
member. Wait for the cluster to become stable before you remove the
next server, to keep the quorum. If `spec.endpoint` gives a foreign
server, change it to a `liken` leader's address before you remove that
server.

## 6. Promote the cluster

After you remove the last foreign member, edit the Cluster resource and
set `spec.origin` to `Founded`:

    kubectl edit cluster

This is the only permitted edit to the field. The promotion makes no
change to the running fleet. It has an effect only if you build the
cluster again: the founding leader of a founded cluster can create the
datastore again.

Only a boot reads `spec.origin`, so the promotion reboots nothing. Each
machine stages the new document and reports `StagedForNextBoot` on its
`ClusterConverged` condition. The machine applies the document at its
next boot, whatever causes that boot. The same holds for an edit to
`spec.endpoint`.
