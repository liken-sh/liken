---
title: Install a cluster
weight: 10
description: "Install a liken cluster from a downloaded release: describe the cluster, mint its identity, build the install stick, boot each machine from it, and reach the cluster with kubectl. Use when creating a cluster on blank x86-64 machines."
---

# Install a cluster

This guide gives the steps from a downloaded release to `kubectl get
nodes`. You do not need the repository, and you do not need to build
the system. The release contains all the files, including the `liken`
toolkit that does these steps.

You need:

* One or more x86-64 machines with blank disks. UEFI firmware and
  BIOS firmware both work. A machine needs 1 GB of memory, and more
  for its workloads. The default disk layout asks for about 20 GB.
  The installation erases the disks it claims.
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

Verify the files that you downloaded. `release.yaml` has a `sha256:`
line for each file in the release, and the release's page on GitHub
gives the digest of `release.yaml` itself:

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

The digest proves that the files match the release page. To prove
also that `liken`'s CI signed them, run the check that [Verify a
release or an image](/docs/guides/verify/) describes. It downloads the
release again:

    ./liken verify release -digest sha256:<hex> <version>

## 2. Describe your cluster

    ./liken new mycluster

[`liken new`](/docs/reference/cli/#liken-new) asks for the names of
your machines, which machines are leaders, their addresses, and their
disks. Then it writes `mycluster/`:
a `cluster.yaml` file and one manifest for each machine. Each field has
a comment that explains what the field means. Keep `mycluster/` in
version control. That directory is the declaration of your cluster.

If you do not know a machine's disks, its network interface names, or
the extra drivers that it needs, write a guess now. The
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

The boot menu and all the messages then go to that port. So you can
install the machine through a serial cable or a remote console. The
machines keep the setting, so their consoles stay available after the
installation.

Check the device name of your USB stick before the next command. The
command overwrites the device.

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

[Write an install stick](/docs/guides/write-an-install-stick/) checks
the copy before a machine boots from it.

## 5. Boot each machine from the stick

Connect the stick and boot the machine. For the first boot, you may
have to open the firmware's boot-device menu. The stick's menu
appears. It has two entries for each machine, and it ends with one
entry for the report:

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
then it waits for you:

    liken: wrote this report to the stick as hardware-report.yaml. Press Enter to reboot.

The report loads only storage and network drivers, which a machine
needs to install itself and join a cluster. Other drivers can take
over hardware that the report uses. A display driver, for example,
takes control of the screen that the report prints to. After the
machine joins the cluster, its `Machine` resource lists the rest of
its hardware in `status.hardware.unclaimed`, and
[Load the drivers for a machine's hardware](/docs/guides/hardware-modules/)
shows how to add their drivers.

Press Enter to reboot the machine. The report changes no disk, so you
can run it as many times as necessary: after you install a disk, after
you connect a cable, or to examine a change before you install.

Take the stick to your workstation and read `hardware-report.yaml`. The
file is a valid Machine manifest. Each line has a comment with the data
that the report found. The comments give the drivers that each device
needs in load order, each disk's size, model, and path, and each
interface's name, MAC address, and link state. Copy the `spec.modules`,
`spec.network`, and `spec.storage` sections into this machine's
manifest in `mycluster/`. Edit the parts marked `CHANGE-ME`. Then build
the stick again with step 4, so that it contains the corrected
manifest.

The report sizes the storage roles from the disks it measured, so you
can install with the sizes unchanged. Two roles are worth checking.
`clusterState` holds the k3s database, the TLS files, and the
containerd image store, so the workloads on the node decide how large
it must be. Increase it if this machine runs many images or large
images. Choose the size before you install: `podStorage` comes after
`clusterState` on the disk, so `clusterState` cannot grow after the
installation.
The Cluster's
[`spec.runtime.kubelet.imageGC`](/docs/reference/cluster/#specruntimekubeletimagegc)
section controls how much of this filesystem the image store may use.
Set `podStorage` to the size that your workloads' volumes need. If the
report had to reduce either size, it says so in the file.

The proposal declares only the network ports that had a cable when the
report ran. It lists the ports with no cable below them, as comments,
with their names and MAC addresses. Remove the comment marks from a
port after you connect its cable. A declared port with no cable delays
each boot, because the machine waits up to thirty seconds for its DHCP
lease.

The report can warn that a disk needs a driver that this image does
not include in its boot modules. You cannot name such a driver in
`spec.modules`, because the machine reads that list only after it
finds its disks. That machine needs an image built with the driver in
its boot modules. The warning names the driver, and the proposal does
not put that disk in the layout.

Run the report on each new machine. It finds the disks, the interface
names, and the drivers that a datasheet does not give.

### Then install the machine

Select `install as <name>` for the machine in front of you. The machine
partitions its blank disks, copies the operating system onto them,
registers itself with its firmware, and then waits for you:

    liken: installed to slot A. Remove the stick, then press Enter to power off. The next power-on boots from the disk.

Remove the stick before you press Enter. The stick is first in the boot
order. If you power on the machine with the stick connected, the menu
appears again. Press Enter. The machine powers off. Power it on again.
From that time, it boots from its own disk.

If the installation fails, it prints the error, lists the machine's
disks, and waits for you:

    liken: press Enter to power off

Correct the cause and boot the installation again. An installation is
idempotent, so a second attempt is safe.

Use the same stick for each machine. Start with the first leader. Each
machine contacts its peers at the addresses that you declared. The
leaders form the control plane, and the followers join it.

### To reinstall a machine that `liken` installed

`install as <name>` claims only blank disks. It refuses a disk that it
does not recognize, so it does not erase data that it did not write.
This protects your data, but it also prevents the plain installation
from replacing an installation that `liken` made. To replace one, select
`wipe and reinstall as <name>`. In one boot, it erases the disks that
this machine's manifest declares, then it installs the machine.
Choosing the entry at the keyboard is the confirmation, and the boot
asks for nothing more. It ends with the same messages as a plain
installation.

A reinstallation erases all the data that it claims, on each disk that
the manifest declares. It erases the cluster state: this node's copy of
the k3s database, its certificates, and the images that it unpacked. It
also erases the volumes that your workloads claimed on this node. The
machine returns as a new member with the same name.

A disk replacement is different. Install a new blank system disk and
select the plain `install as <name>`. The installation claims the
blank disk and recognizes the data disk that it wrote before. So the
cluster state on that disk stays. Only `wipe and reinstall` erases a
disk that `liken` claimed.

If you reinstall with a different storage layout, the `Machine`
resource in the cluster still describes the old one. A storage role
can only grow, so when the reinstall made a partition larger than the
old spec declares, the machine reports `SpecConverged: False` with the
reason `StagingRejected` and gives the size that the partition has
now. To fix it, first let the machine boot and
publish its status. Then edit the `Machine` resource to the new
layout. The API server compares your edit with the sizes that the
machine last booted with, so it accepts the edit only after the
machine has reported them. Change the machine's manifest in
`mycluster/` to the same layout too, because the next stick and the
next reinstall start from that copy.

## 6. Talk to your cluster

    ./liken kubeconfig mycluster

[`liken kubeconfig`](/docs/reference/cli/#liken-kubeconfig) writes
`mycluster/identity/kubeconfig`, an administrator credential. It points
at the `endpoint:` in your `cluster.yaml`. If your workstation reaches
the cluster at a different address, pass `-server`:

    ./liken kubeconfig -server https://203.0.113.5:6443 mycluster

Then:

    kubectl --kubeconfig mycluster/identity/kubeconfig get nodes

Each machine shows as Ready. The cluster is now an ordinary Kubernetes
cluster, with two more `liken` resources:

    kubectl get clusters      what the fleet is, as one document
    kubectl get machines      each machine, as the OS sees it

Edit those resources to make configuration changes. The
[Machine](/docs/reference/machine/) and
[Cluster](/docs/reference/cluster/) pages describe each field. When a
new release is available, [Upgrade the fleet](/docs/guides/upgrade/)
moves each machine to it with one edit. If a step did not go as this
guide says, [Troubleshoot](/docs/guides/troubleshoot/) starts from
the symptom.
