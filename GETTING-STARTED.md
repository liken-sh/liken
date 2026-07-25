# Getting started

This document gives the steps from an empty directory to
`kubectl get nodes`. It is for a person who downloaded a liken release
and has machines to install it on. You do not need this repository,
and you do not need to build the system. The release contains all the
files, including the `liken` toolkit that does these steps. The manual
gives the long form of this procedure:
<https://liken.sh/docs/guides/install/>.

## 1. Download a release and check it

A release has nine files and one document that names them:

    vmlinuz               the Linux kernel
    liken.sqfs            the operating system: a read-only filesystem
                          image a machine mounts as its root
    boot.cpio             the small initramfs: init and the early
                          boot's kernel modules
    microcode.cpio        CPU microcode, loaded before everything else
    liken                 the toolkit
    systemd-bootx64.efi   the install stick's boot menu, for UEFI
    grub-boot.img         the BIOS boot loader's first stage
    grub-core.img         the BIOS boot loader's second stage
    LICENSES.md           third-party license notices
    release.yaml          each of the above, by sha256 digest, and
                          which kernel, k3s, and other components are
                          inside

Download all nine files and the document into one directory. The
toolkit checks each file against the document, so a missing file stops
the build of the stick.

A release's version is the date of the release, plus a serial number:
for example, 2026.07.11-001. The version gives no more than that. To
see what is in a release, read the `components` section of its
`release.yaml`.

Verify the files that you downloaded against the document.
`release.yaml` contains a `sha256:` line for each file. The release's
page also gives the digest of `release.yaml` itself. Thus the chain of
trust starts with a value that you can read on the site:

    sha256sum vmlinuz liken.sqfs boot.cpio microcode.cpio liken \
        systemd-bootx64.efi grub-boot.img grub-core.img LICENSES.md

Then make the toolkit executable:

    chmod +x liken

To download and check a whole release with one command, use `liken
fetch`. The manual gives the steps:
<https://liken.sh/docs/guides/install/>.

## 2. Describe your cluster

    ./liken new mycluster

This command asks approximately twelve simple questions: the names of
your machines, which machines are leaders, their addresses, and their
disks. Then it writes `mycluster/`: a `cluster.yaml` file and one
manifest for each machine. Each field has a comment that explains what
the field means and why it is there. Thus the directory also shows you
how to change it. Keep `mycluster/` in version control. That directory
is the declaration of your cluster.

## 3. Mint the cluster's identity

    ./liken mint mycluster/identity

The identity is the set of certificate authorities and the join token
that make your machines into one cluster. The files include private
keys. The scaffold's `.gitignore` file keeps them out of version
control. If you already run a k3s cluster, do not run `liken mint`. Use
`liken adopt` instead, to join machines to that cluster. To list and
read about each command, run `./liken` with no arguments.

## 4. Pack your layer and build the stick

    ./liken layer mycluster mycluster/identity mycluster/deployment.cpio
    ./liken stick . mycluster/deployment.cpio mycluster/install.img

The layer is the small archive that contains everything that is yours:
your manifests and your identity. The `stick` command joins the
release that you downloaded with your layer into one bootable disk
image. Here `.` is the directory that contains `release.yaml` and
every file that the document names.

For a machine with no screen, name its serial port:

    ./liken stick -console ttyS0 . mycluster/deployment.cpio mycluster/install.img

The boot menu and all the messages then go to that port. The machines
keep the setting after the installation.

Write the image to a USB stick. Check the device name first. This
command overwrites the device.

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

## 5. Boot each machine from the stick

A person must attend each installation. Give each machine a keyboard
and a screen, or a serial console. A person selects the boot entry and
answers the message at the end. After the installation, the machine
needs no keyboard and no screen.

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

Run the hardware report before you install a machine. Select `liken
hardware report`. This boot changes no disk. It writes a proposed
manifest to the stick as `hardware-report.yaml`, with the machine's
`spec.modules`, `spec.network`, and `spec.storage`. Copy those sections
into the machine's manifest in `mycluster/`, then build the stick again
with step 4. The manual gives the steps:
<https://liken.sh/docs/guides/install/#first-run-the-hardware-report>.

To install, select `install as <name>` for the machine in front of you.
The machine partitions its blank disks, copies the operating system
onto them, registers itself with its firmware, then it holds:

    liken: installed to slot A; remove the stick, then press Enter to power off; the next power-on boots from the disk.

Remove the stick, then press Enter. The machine powers off. Power it on
again. From that time, it boots from its own disk. Use the same stick
for each machine. Start with the first leader.

`install as <name>` claims blank disks only. To replace an installation
that liken made, select `wipe and reinstall as <name>`. That entry
erases the disks that the machine's manifest declares, then it installs
the machine.

The machines find each other at the addresses that you declared. The
leaders make the control plane, and the followers join it.

## 6. Talk to your cluster

    ./liken kubeconfig mycluster/identity

This command writes `mycluster/identity/kubeconfig`, an administrator
credential. The file points at `https://127.0.0.1:16443`, the address
that the development lab uses. Change its `server:` line to your
cluster's endpoint: the `endpoint:` value in your `cluster.yaml`. Then
run:

    kubectl --kubeconfig mycluster/identity/kubeconfig get nodes

Each machine shows as Ready. The cluster is now an ordinary Kubernetes
cluster, with two more liken resources:

    kubectl get clusters      what the fleet is, as one document
    kubectl get machines      each machine, as the OS sees it

Edit those resources to make configuration changes. When a new liken
release is available, you move the whole fleet to it with an edit to
two fields on the Cluster. The comments on `spec.releases` explain the
fields. This upgrade needs no new media and no work on each machine.
Each machine downloads, verifies, and proves the new version itself,
one machine at a time.

To make a git repository the source of these edits, declare the `flux`
feature. The cluster then syncs your manifests and your workloads from
the repository. The manual gives the steps:
<https://liken.sh/docs/guides/gitops/>.
