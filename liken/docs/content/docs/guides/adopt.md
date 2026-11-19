---
title: Adopt an existing k3s cluster
weight: 20
description: "Join liken machines to an existing k3s cluster with embedded etcd and replace its machines one at a time while the cluster serves. Use when moving a cluster onto liken without exporting or restoring its state."
---

# Adopt an existing k3s cluster

Adoption joins `liken` machines to a k3s cluster that `liken` did not
create, so you can replace the cluster's machines one at a time while
the cluster keeps serving. You export and restore nothing, because
each new member gets the data through etcd replication. Adoption
works with any k3s cluster that uses embedded etcd, on any operating
system.

A `liken` server turns off k3s's bundled traefik, servicelb, and
metrics-server unless your `cluster.yaml` declares them as features,
and this applies to the whole cluster as soon as the first `liken`
server joins. If your workloads need one of these components, declare
its feature in your `cluster.yaml`, or deploy a replacement before
that first join.

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
directory, for example `harvest/`. The archive has only the
certificate authorities and the join token. It needs no server
certificates, because each server signs its own with the shared
certificate authorities.

## 2. Arrange the identity

    ./liken new mycluster
    ./liken adopt harvest mycluster/identity

[`liken adopt`](/docs/reference/cli/#liken-adopt) puts the harvested
files into the identity directory in the same layout as
[`liken mint`](/docs/reference/cli/#liken-mint) writes. It refuses an
incomplete harvest and a token that does not match the harvested
certificate authority. From here on, the steps are the same as for a
minted identity.

## 3. Declare the adoption

Edit `mycluster/cluster.yaml`:

* Set [`spec.origin`](/docs/reference/cluster/#spec--origin) to
  `Adopted`.
* Set `spec.endpoint` to the existing cluster's join URL.

With `Adopted`, every `liken` leader joins the existing datastore
through the endpoint, and no `liken` machine creates a new one. A
second datastore beside the live one would split the cluster in
two.

### The document claims the whole cluster

[`spec.features`](/docs/reference/cluster/#spec--features) is the
complete list of features for the whole cluster, including the
machines you did not install. `liken` turns off and removes each
feature that the list does not have.

So when the cluster already runs Flux and your `cluster.yaml` does
not declare `flux`, `liken` treats Flux as turned off. It does not
delete that Flux, because it removes a Flux installation only when
the `flux-system` namespace has the annotation
`liken.sh/feature=flux`, and it writes that annotation only when
`cluster.yaml` declares the feature. Instead, the Cluster reports that
it deleted nothing:

    kubectl describe cluster

The `FluxTeardown` condition is `False` with the reason
`NotPlantedByLiken`, and its message gives the command that hands the
installation to `liken`. Decide before you install the first machine:

* To keep the existing Flux and run it yourself, leave `flux` out of
  `spec.features`. The sync keeps running, and the condition stays.
* To run the fleet from git with `liken`, declare the `flux` feature.
  [Run the fleet from git](/docs/guides/gitops/) has the steps, and
  its last section covers an installation that already exists.

## 4. Install the `liken` machines

Build the stick and install each machine as in
[Install a cluster](/docs/guides/install/). Start with the first
leader. Each machine joins the existing cluster, and the existing
machines keep serving.

`liken` machines have the node label `liken.sh/machine=true`, and the
OS workloads run only on nodes with that label, so they do not run on
the existing nodes.

## 5. Rotate the old servers out

Remove the old servers one at a time:

    kubectl delete node <old-server>

For a k3s server, the deletion of the node also removes the etcd
member. To keep etcd's quorum, wait until the cluster is stable
before you remove the next server. If `spec.endpoint` points at an old
server, change it to a `liken` leader's address before you remove
that server.

## 6. Promote the cluster

After you remove the last old server, edit the Cluster and set
`spec.origin` to `Founded`:

    kubectl edit cluster

This change from `Adopted` to `Founded` is the only edit the API
allows to the field. It changes nothing on the running fleet. It
matters only when you build the cluster again from nothing, because
the founding leader of a `Founded` cluster can create a new datastore.

Only a boot reads `spec.origin`, so the edit reboots nothing. Each
machine stages the new document, reports the reason
`StagedForNextBoot` on its `ClusterConverged` condition, and applies
the document at its next boot, whatever causes that boot. An edit to
`spec.endpoint` works the same way.
