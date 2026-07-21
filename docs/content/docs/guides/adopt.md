---
title: Adopt an existing k3s cluster
weight: 20
---

# Adopt an existing k3s cluster

Adoption joins `liken` machines to a k3s cluster that `liken` did not
create. It lets you replace the cluster's machines one at a time,
while the cluster keeps serving. The process never exports or
restores cluster state. Each new member receives the data through
ordinary replication.

Adoption works with any k3s cluster that uses embedded etcd,
whatever operating system it runs on today.

Know one behavior before you start. A `liken` server disables k3s's
bundled components (traefik, servicelb, metrics-server) unless your
`cluster.yaml` declares them as features. The disable acts on the
whole cluster the moment the first `liken` server joins. If your
workloads depend on a bundled component, declare its feature in your
`cluster.yaml`, or run a replacement before that first join.

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
directory, for example `harvest/`. Only the certificate authorities
and the join token come over. The server's leaf certificates stay
behind, because every server signs its own from the shared roots.

## 2. Arrange the identity

    ./liken new mycluster
    ./liken adopt harvest mycluster/identity

[`liken adopt`](/docs/reference/cli/#liken-adopt) places the
harvested files into the identity directory exactly as
[`liken mint`](/docs/reference/cli/#liken-mint) would have. It refuses a partial harvest, and it
checks that the token matches the harvested certificate authority.
After this step, nothing downstream cares where the identity came
from.

## 3. Declare the adoption

Edit `mycluster/cluster.yaml`:

* Set [`spec.origin`](/docs/reference/cluster/#spec) to `adopted`.
* Set `spec.endpoint` to the existing cluster's join URL.

An adopted cluster's datastore already exists. Every `liken` leader
joins it through the endpoint, and no `liken` machine initializes a
new one. Initializing a second datastore beside a live one would
split the cluster into two.

## 4. Install the liken machines

Build the stick and install each machine as in
[Install a cluster](/docs/guides/install/), starting with the first
leader. Each machine joins the existing cluster directly. The
existing machines keep serving throughout.

`liken` machines carry a `liken.sh/machine=true` node label, and the
OS workloads schedule only onto labeled nodes. The foreign nodes stay
untouched.

## 5. Rotate the old servers out

Remove the foreign servers one at a time:

    kubectl delete node <old-server>

For a k3s server, deleting the node is also the etcd member removal.
Wait for the cluster to settle before you remove the next one, so
that quorum holds. If `spec.endpoint` points at a foreign server,
edit it to a `liken` leader's address before you remove that server.

## 6. Promote the cluster

After the last foreign member is gone, edit the Cluster resource and
set `spec.origin` to `founded`:

    kubectl edit cluster

This is the field's one legal edit. Promotion changes nothing on the
running fleet. It matters if you ever rebuild the cluster from
scratch: a founded cluster's founding leader may create the datastore
again.
