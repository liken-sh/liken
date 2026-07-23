# Getting started

This document describes the steps from an empty directory to
`kubectl get nodes`. It is written for a person who has downloaded a
liken release and has machines to install it on. None of these steps
need this repository or a build. The release carries everything you
need, including the `liken` toolkit that runs these steps.

## 1. Download a release and check it

A release has five files and one document that names them:

    vmlinuz               the Linux kernel
    liken.sqfs            the operating system, nobody's in particular:
                          a read-only filesystem image a machine mounts
                          as its root
    boot.cpio             the small initramfs the boot loader stages —
                          init and the early boot's modules
    liken                 the toolkit
    systemd-bootx64.efi   the install stick's boot menu
    release.yaml          each of the above, by sha256 digest, and
                          which kernel, k3s, and friends are inside

A release's version is the date it was cut, plus a serial number: for
example, 2026.07.11-001. The version carries nothing more than that
on purpose. To see what shipped in a release, read the `components`
section of its `release.yaml`.

Verify what you downloaded against the document. `release.yaml` holds
a `sha256:` line for each file. The release's page also publishes the
digest of `release.yaml` itself, so the chain of trust starts with
something you can read on the site:

    sha256sum vmlinuz liken.sqfs boot.cpio liken systemd-bootx64.efi

Then make the toolkit runnable:

    chmod +x liken

## 2. Describe your cluster

    ./liken new mycluster

This command asks about a dozen plain questions. It asks what your
machines are called, which machines are leaders, their addresses, and
their disks. Then it writes `mycluster/`: a `cluster.yaml` file and
one manifest for each machine. Every field carries a comment that
explains what the field means and why it is there. Because of these
comments, the directory you get also documents how to change it. Keep
`mycluster/` in version control. It is your cluster, declared.

## 3. Mint the cluster's identity

    ./liken mint mycluster/identity

The identity is the set of certificate authorities and the join
token that make your machines into one cluster. The files include
private keys. The scaffold's `.gitignore` file already keeps these
files out of version control. To join machines to a k3s cluster you
already run, use `liken adopt` instead. Running `./liken` with no
arguments lists and explains every command.

## 4. Pack your layer and build the stick

    ./liken layer mycluster mycluster/identity mycluster/deployment.cpio
    ./liken stick . mycluster/deployment.cpio mycluster/install.img

The layer is the small archive that holds everything that is yours:
your manifests and your identity. The `stick` command joins the
release you downloaded (`.` here is the directory that holds
`release.yaml`) with your layer into one bootable disk image.

Write the image to a USB stick. Check the device name first: this
command overwrites the device.

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

## 5. Boot each machine from the stick

Plug in the stick and boot the machine. The first time, you may need
to open the firmware's boot-device menu. A menu then appears that
lists your machines by name:

    install as big
    install as little
    install as tiny

Pick the entry for the machine in front of you. The machine
partitions its own blank disks, copies the operating system onto
them, registers itself with its firmware, and powers off. Unplug the
stick and power the machine back on. From then on, it boots from its
own disk. Use the same stick for every machine, starting with the
first leader.

The machines find each other at the addresses you declared. The
leaders form the control plane, and the followers join it.

## 6. Talk to your cluster

    ./liken kubeconfig mycluster/identity

This command writes `mycluster/identity/kubeconfig`, an administrator
credential. The file points at `https://127.0.0.1:16443`, which is
the address used in the development lab. Edit its `server:` line to
your cluster's endpoint: the `endpoint:` value in your `cluster.yaml`.
Then run:

    kubectl --kubeconfig mycluster/identity/kubeconfig get nodes

Every machine shows as Ready. From here, it is an ordinary Kubernetes
cluster, plus two liken resources worth knowing:

    kubectl get clusters      what the fleet is, as one document
    kubectl get machines      each machine, as the OS sees it

You make configuration changes by editing those resources. When a new
liken release comes out, you move the whole fleet to it by editing
two fields on the Cluster. The comments on `spec.releases` explain
the fields. This upgrade needs no rebuilt media and no per-machine
work. Each machine fetches, verifies, and proves the new version
itself, one machine at a time.

To make a git repository the source of these edits, declare the
`flux` feature. The cluster then syncs your manifests and your
workloads from the repository. The manual has the steps:
<https://liken.sh/docs/guides/gitops/>.
