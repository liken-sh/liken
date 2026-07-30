---
title: Install a cluster
weight: 10
---

# Install a cluster

This guide gives the steps from a downloaded release to `kubectl get
nodes`. You do not need the repository, and you do not need to build
the system. The release contains all the files, including the `liken`
toolkit that does these steps.

You need:

* One or more machines with blank disks. The installation erases the
  disks it claims.
* A USB stick. The installation image overwrites it.
* A Linux workstation with `kubectl`.
* A keyboard and a screen on each machine, for the installation. A
  person selects the boot entry and answers the message at the end. If
  a machine has no screen, use a serial console. Build the stick with
  `-console`, as step 4 shows.

After the installation, the machine needs nothing attached. It boots,
joins the cluster, and gets its configuration from the cluster.

## 1. Download a release

Releases are at [releases.liken.sh](https://releases.liken.sh/).
Each release is a directory with the name of its version, for example
`2026.07.20-001/`. Select a version, then download the toolkit and the
release document:

    curl -fLO https://releases.liken.sh/<version>/liken
    curl -fLO https://releases.liken.sh/<version>/release.yaml

Verify the files that you downloaded. `release.yaml` contains a
`sha256:` line for each file in the release. The release's page on
GitHub gives the digest of `release.yaml` itself. Thus the chain of
trust starts with a value that you can read:

    sha256sum release.yaml liken

Compare the first digest with the release page. Compare the second
digest with the `liken` entry in `release.yaml`. Then make the toolkit
executable, and download and verify the rest of the release:

    chmod +x liken
    ./liken fetch -digest sha256:<hex> https://releases.liken.sh <version> channel

The `-digest` value is the digest of `release.yaml` from the release
page. [`liken fetch`](/docs/reference/cli/#liken-fetch) writes the
whole release into `channel/<version>/`, and it checks each byte
against the document. [The release
channel](/docs/reference/release-channel/) describes the layout and
the trust chain.

## 2. Describe your cluster

    ./liken new mycluster

[`liken new`](/docs/reference/cli/#liken-new) asks approximately twelve
simple questions: the names of your machines, which machines are
leaders, their addresses, and their disks. Then it writes `mycluster/`:
a `cluster.yaml` file and one manifest for each machine. Each field has
a comment that explains what the field means. Keep `mycluster/` in
version control. That directory is the declaration of your cluster.

If you do not know a machine's disks, its network interface names, or
the extra drivers that it needs, write your best estimate now. The
hardware report in step 5 boots the machine, examines its hardware, and
writes a corrected manifest.

## 3. Mint the cluster's identity

    ./liken mint mycluster/identity

[`liken mint`](/docs/reference/cli/#liken-mint) creates the identity:
the set of certificate authorities and the join token that make your
machines into one cluster. The files include private keys. The
scaffold's `.gitignore` keeps them out of version control.

If you already run a k3s cluster, do not run `liken mint`. Use
[`liken adopt`](/docs/reference/cli/#liken-adopt) instead, to join
machines to that cluster. [Adopt an existing k3s
cluster](/docs/guides/adopt/) gives the steps.

## 4. Build the install stick

    ./liken layer mycluster mycluster/identity mycluster/deployment.cpio
    ./liken stick channel/<version> mycluster/deployment.cpio mycluster/install.img

[`liken layer`](/docs/reference/cli/#liken-layer) packs the layer: the
small archive that contains your manifests and your identity.
[`liken stick`](/docs/reference/cli/#liken-stick) joins the release
with your layer into one bootable disk image.

For a machine with no screen, name its serial port when you build the
stick:

    ./liken stick -console ttyS0 channel/<version> mycluster/deployment.cpio mycluster/install.img

The boot menu and all the messages then go to that port. Thus you can
install the machine through a serial cable or a remote console. The
machines keep the setting, so their consoles stay available after the
installation.

Check the device name of your USB stick before the next command. The
command overwrites the device.

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

## 5. Boot each machine from the stick

Connect the stick and boot the machine. For the first boot, it is
possible that you must open the firmware's boot-device menu. The
stick's menu appears. It has two entries for each machine, and it ends
with one entry for the report:

    install as big
    wipe and reinstall as big
    install as little
    wipe and reinstall as little
    liken hardware report

The menu has no timeout. You must select an entry.

### First, run the hardware report

Select `liken hardware report`. This boot makes no changes to the
machine's disks. It loads the drivers for the disks and the network
ports, records the devices that appear, and writes a proposed manifest
to the stick as `hardware-report.yaml`. It prints the full proposal,
then it holds:

    liken: this report was written to the stick as hardware-report.yaml; press Enter to reboot.

The report loads storage drivers and network drivers only. A machine
needs these two types of driver to install itself and to join a
cluster. A driver for other hardware would change the machine while a
person is in front of it. A display driver, for example, takes control
of the screen that the report prints to. The machine reports its other
hardware in the node's status after the node runs, in the list of
unclaimed hardware.

Press Enter to reboot the machine. The report changes no disk, so you
can run it as many times as necessary: after you install a disk, after
you connect a cable, or to examine a change before you install.

Take the stick to your workstation and read `hardware-report.yaml`. The
file is a valid Machine manifest. Each line has a comment with the data
that the report found: the drivers that each device needs, in load
order; each disk's size, model, and path; each interface's name, MAC
address, and link state. Copy the `spec.modules`, `spec.network`, and
`spec.storage` sections into this machine's manifest in `mycluster/`.
Edit the parts marked `CHANGE-ME`. Then build the stick again with step
4, so that it contains the corrected manifest.

Three parts of the proposal need your judgement.

The storage sizes agree with the disks that the report measured, so you
can install with them unchanged. Two roles still need your attention.
`clusterState` contains the k3s database, the TLS files, and the
containerd image store. Thus the workloads on the node set its size.
Increase it if this machine runs many images, or large images. Select
this number carefully. The Cluster's
`spec.runtime.kubelet.imageGC` section controls how much of this
filesystem the image store keeps. `podStorage` comes after
`clusterState` on the disk, so `clusterState` cannot become larger
after the installation.
Set `podStorage` to the size that your workloads' volumes need. If the
report had to decrease either size, it says so in the file.

The proposal declares only the network ports that had a cable when the
report ran. It lists the ports with no cable below them, as comments,
with their names and MAC addresses. Remove the comment marks from a
port after you connect its cable. A declared port with no cable delays
each boot, because the machine waits a maximum of thirty seconds for
its DHCP lease.

The report can give a warning that a disk needs a driver that this
image does not contain on its boot path. You cannot put such a driver
in `spec.modules`, because the machine reads that list only after it
finds its disks. That machine needs an image built with the driver in
its boot modules. The proposal gives the name of the driver, and it
does not put that disk in the layout.

Run the report on each new machine. It gives you the disks, the
interface names, and the drivers that a datasheet does not give.

### Then install the machine

Select `install as <name>` for the machine in front of you. The machine
partitions its blank disks, copies the operating system onto them,
registers itself with its firmware, then it holds:

    liken: installed to slot A; remove the stick, then press Enter to power off; the next power-on boots from the disk.

Remove the stick before you press Enter. The stick is first in the boot
order. If you power on the machine with the stick connected, the menu
appears again. Press Enter. The machine powers off. Power it on again.
From that time, it boots from its own disk.

If the installation does not complete, it prints the error, lists the
machine's disks, and holds:

    liken: press Enter to power off

Correct the cause and boot the installation again. An installation is
idempotent, so a second attempt is safe.

### To reinstall a machine that liken installed

`install as <name>` claims only blank disks. It refuses a disk that it
does not recognize, so it does not erase data that it did not write.
This protects your data, but it also prevents the plain installation
from replacing an installation that liken made. To replace one, select
`wipe and reinstall as <name>`. In one boot, it erases the disks that
this machine's manifest declares, then it installs the machine. Your
selection of the entry at the keyboard is the confirmation. It ends
with the same held messages as a plain installation.

A reinstallation erases all the data that it claims, on each disk that
the manifest declares. It erases the cluster state: this node's copy of
the k3s database, its certificates, and the images that it unpacked. It
also erases the volumes that your workloads claimed on this node. The
machine returns as a new member with the same name, and not as the
member it was.

A disk replacement is different. If you install a new blank system disk
and select the plain `install as <name>`, the installation claims the
blank disk and recognizes the data disk that it wrote before. Thus the
cluster state on that disk stays. Only `wipe and reinstall` erases a
disk that liken claimed.

If you reinstall with a different layout, the machine's document in the
cluster continues to describe the old layout, and the two disagree.
Storage roles can only increase in size, so the machine reports
`SpecConverged: False` with the reason `StagingRejected`, and it gives
the size that the disk now has. Correct this in this order. First, let
the machine boot and publish its status. Then edit the Machine resource
in the cluster to the layout that it now has. The rule compares your
spec with the sizes that the machine last booted with, so the cluster
accepts the edit only after the machine reports them. Also change the
machine's manifest in `mycluster/` to the same layout, because the next
stick and the next reinstallation start from that copy.

Use the same stick for each machine. Start with the first leader. The
machines find each other at the addresses that you declared. The
leaders make the control plane, and the followers join it.

## 6. Talk to your cluster

    ./liken kubeconfig mycluster/identity

[`liken kubeconfig`](/docs/reference/cli/#liken-kubeconfig) writes
`mycluster/identity/kubeconfig`, an administrator credential. Change
its `server:` line to your cluster's endpoint: the `endpoint:` value in
your `cluster.yaml`. Then:

    kubectl --kubeconfig mycluster/identity/kubeconfig get nodes

Each machine shows as Ready. The cluster is now an ordinary Kubernetes
cluster, with two more `liken` resources:

    kubectl get clusters      what the fleet is, as one document
    kubectl get machines      each machine, as the OS sees it

Edit those resources to make configuration changes. The
[Machine](/docs/reference/machine/) and
[Cluster](/docs/reference/cluster/) pages describe each field. When a
new release is available, [Upgrade the fleet](/docs/guides/upgrade/)
moves each machine to it with one edit.
