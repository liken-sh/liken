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

If you do not know a machine's disks, its network interface names, or
the extra drivers it needs, fill in your best guess now. The hardware
report in step 5 boots the machine, observes its hardware, and writes
a corrected manifest for you.

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
to open the firmware's boot-device menu. The stick's menu appears. It
gives each machine two entries, and it ends with one entry for the
report:

    install as big
    wipe and reinstall as big
    install as little
    wipe and reinstall as little
    liken hardware report

The menu never times out. You must pick an entry.

### First, run the hardware report

Pick `liken hardware report`. This boot changes nothing on the
machine's disks. It loads the drivers the hardware wants, watches
which disks and network interfaces appear, and writes a proposed
manifest to the stick as `hardware-report.yaml`. It prints the whole
proposal, then holds:

    liken: this report was written to the stick as hardware-report.yaml; press Enter to reboot.

Press Enter to reboot the machine. Take the stick to your
workstation. Read `hardware-report.yaml`. It is a valid Machine
manifest with the evidence for each line beside it as a comment: the
drivers each device wants, in load order; each disk's size, model, and
path; each interface's name, MAC, and link state. Copy the
`spec.modules`, `spec.network`, and `spec.storage` sections into this
machine's manifest in `mycluster/`. Edit the parts marked `CHANGE-ME`,
then rebuild the stick with step 4, so it carries the corrected
manifest.

Three parts of the proposal need your judgement.

The storage sizes fit the disks the report measured, so you can
install from them as they are. Two roles still deserve a look.
`clusterState` holds k3s's database, its TLS material, and
containerd's image store, so what the node runs decides its size.
Raise it if this machine runs many images, or large ones. Set
`podStorage` to the size your workloads' volumes need. The report says
so in the file when it had to reduce either one.

The proposal declares only the network ports that had a cable when the
report ran. It lists the dark ports below them, commented out, with
their names and MAC addresses. Uncomment a port after you connect its
cable. A declared port with no cable delays every boot, because the
machine waits up to thirty seconds for its DHCP lease.

The report may warn that a disk needs a driver that this image does
not carry on its boot path. Such a driver cannot go in `spec.modules`,
because the machine reads that list only after it has already found
its disks. That machine needs an image built with the driver in its
boot modules. The proposal says which driver, and leaves that disk
out of the layout.

Run the report on each new machine. Its answers are the disks, the
interface names, and the drivers you cannot know from a datasheet.

### Then install the machine

Pick `install as <name>` for the machine in front of you. The machine
partitions its blank disks, copies the operating system onto them,
registers itself with its firmware, and holds:

    liken: installed to slot A; remove the stick, then press Enter to power off; the next power-on boots from the disk.

Remove the stick before you press Enter. The stick is first in the
boot order, so a power-on with the stick still in returns to the menu.
Press Enter. The machine powers off. Power it back on. From then on,
it boots from its own disk.

If the install cannot finish, it prints the error, lists the machine's
disks, and holds:

    liken: press Enter to power off

Fix the cause and boot the install again. An install is idempotent, so
a second attempt is safe.

### To reinstall a machine liken already wrote

`install as <name>` claims only blank disks. It refuses a disk it does
not recognize, so it never erases data it did not write. This protects
you, but it also means the plain install cannot replace an install
liken itself made. To do that, pick `wipe and reinstall as <name>`. It
blanks the disks this machine's manifest declares, then installs, in
one boot. Picking the entry at the keyboard is your confirmation. It
ends at the same held messages as a plain install.

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
