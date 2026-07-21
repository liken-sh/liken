---
title: Install a cluster
weight: 10
---

# Install a cluster

This guide takes you from a downloaded release to `kubectl get
nodes`. You do not need the repository or a build. The release
carries everything, including the `liken` toolkit that runs these
steps.

You need:

* One or more machines with blank disks. The install erases the
  disks it claims.
* A USB stick. The install image overwrites it.
* A Linux workstation with `kubectl`.

## 1. Download a release

Releases live at [releases.liken.sh](https://releases.liken.sh/).
Each release is a directory named by its version, for example
`2026.07.20-001/`. Pick a version, then download the toolkit and the
release document:

    curl -fLO https://releases.liken.sh/<version>/liken
    curl -fLO https://releases.liken.sh/<version>/release.yaml

Verify what you downloaded. `release.yaml` holds a `sha256:` line for
each file in the release. The release's page on GitHub publishes the
digest of `release.yaml` itself, so the chain of trust starts with
something you can read:

    sha256sum release.yaml liken

Compare the first digest against the release page. Compare the
second against the `liken` entry in `release.yaml`. Then make the
toolkit runnable, and let it download and verify the rest of the
release:

    chmod +x liken
    ./liken fetch -digest sha256:<hex> https://releases.liken.sh <version> channel

The `-digest` value is the digest of `release.yaml` from the release
page. [`liken fetch`](/docs/reference/cli/#liken-fetch) writes the
whole release into `channel/<version>/`, and it checks every byte
against the document. [The release
channel](/docs/reference/release-channel/) describes the layout and
the trust chain.

## 2. Describe your cluster

    ./liken new mycluster

[`liken new`](/docs/reference/cli/#liken-new) asks about a dozen
plain questions: what your machines are called, which machines are
leaders, their addresses, and their disks. Then it writes `mycluster/`: a `cluster.yaml` file and one
manifest for each machine. Every field carries a comment that
explains what the field means. Keep `mycluster/` in version control.
It is your cluster, declared.

## 3. Mint the cluster's identity

    ./liken mint mycluster/identity

[`liken mint`](/docs/reference/cli/#liken-mint) creates the identity:
the set of certificate authorities and the join token that make your
machines into one cluster. The files include private keys. The
scaffold's `.gitignore` already keeps them out of version control.

To join machines to a k3s cluster you already run, use
[`liken adopt`](/docs/reference/cli/#liken-adopt) instead.
[Adopt an existing k3s cluster](/docs/guides/adopt/) has the steps.

## 4. Build the install stick

    ./liken layer mycluster mycluster/identity mycluster/deployment.cpio
    ./liken stick channel/<version> mycluster/deployment.cpio mycluster/install.img

[`liken layer`](/docs/reference/cli/#liken-layer) packs the layer:
the small archive that holds everything that is yours, your
manifests and your identity.
[`liken stick`](/docs/reference/cli/#liken-stick) joins the release
with your layer into one bootable disk image.

Check the device name of your USB stick before the next command. The
command overwrites the device.

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

## 5. Boot each machine from the stick

Plug in the stick and boot the machine. The first time, you may need
to open the firmware's boot-device menu. A menu appears that lists
your machines by name:

    install as big
    install as little
    install as tiny

Pick the entry for the machine in front of you. The machine
partitions its blank disks, copies the operating system onto them,
registers itself with its firmware, and powers off. Unplug the stick
and power the machine back on. From then on, it boots from its own
disk.

Use the same stick for every machine. Start with the first leader.
The machines find each other at the addresses you declared. The
leaders form the control plane, and the followers join it.

## 6. Talk to your cluster

    ./liken kubeconfig mycluster/identity

[`liken kubeconfig`](/docs/reference/cli/#liken-kubeconfig) writes
`mycluster/identity/kubeconfig`, an administrator credential. Edit its `server:` line to your cluster's endpoint: the
`endpoint:` value in your `cluster.yaml`. Then:

    kubectl --kubeconfig mycluster/identity/kubeconfig get nodes

Every machine shows as Ready. From here, it is an ordinary Kubernetes
cluster, plus two `liken` resources worth knowing:

    kubectl get clusters      what the fleet is, as one document
    kubectl get machines      each machine, as the OS sees it

You make configuration changes by editing those resources. The
[Machine](/docs/reference/machine/) and
[Cluster](/docs/reference/cluster/) pages describe every field. When
a new release comes out, [Upgrade the fleet](/docs/guides/upgrade/)
moves every machine to it with one edit.
