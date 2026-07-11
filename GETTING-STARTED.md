# Getting started

This is the walk from an empty directory to `kubectl get nodes`,
written for a person with a downloaded liken release and some
machines to install it on. Nothing here needs this repository or a
build — the release carries everything, including the `liken`
toolkit these steps run.

## 1. Download a release and check it

A release is four files and a document that names them:

    vmlinuz               the Linux kernel
    liken.cpio            the operating system, nobody's in particular
    liken                 the toolkit
    systemd-bootx64.efi   the install stick's boot menu
    release.yaml          each of the above, by sha256 digest, and
                          which kernel, k3s, and friends are inside

A release's version is the date it was cut plus a serial —
2026.07.11-001 — and deliberately nothing more; `release.yaml`'s
`components` section is where you read what shipped in it.

Verify what you downloaded against the document — `release.yaml`
holds a `sha256:` line for each file, and the release's page
publishes the digest of `release.yaml` itself, so the chain starts
with something you can read on the site:

    sha256sum vmlinuz liken.cpio liken systemd-bootx64.efi

Then make the toolkit runnable:

    chmod +x liken

## 2. Describe your cluster

    ./liken new mycluster

This asks a dozen plain questions — what your machines are called,
which are leaders, their addresses, their disks — and writes
`mycluster/`: a `cluster.yaml` and one manifest per machine. Every
field carries a comment explaining what it means and why it's there,
so the directory you get is also the documentation for changing it.
Keep it in version control; it is your cluster, declared.

## 3. Mint the cluster's identity

    ./liken mint mycluster/identity

The identity is the set of certificate authorities and the join
token that make your machines one cluster. The files include private
keys — the scaffold's `.gitignore` already keeps them out of version
control. (To join machines to a k3s cluster you already run, use
`liken adopt` instead; running `./liken` with no arguments explains
every command.)

## 4. Pack your layer and build the stick

    ./liken layer mycluster mycluster/identity - mycluster/deployment.cpio
    ./liken stick . mycluster/deployment.cpio mycluster/install.img

The layer is the small archive holding everything that is yours:
your manifests and your identity. (The `-` stands in for a kernel
directory, which is only consulted if a machine manifest declares
extra kernel modules; the scaffold doesn't.) The stick command joins
the release you downloaded (`.` here, the directory release.yaml is
in) with your layer into one bootable disk image.

Write it to a USB stick — double-check the device name, this
overwrites it:

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

## 5. Boot each machine from the stick

Plug the stick in, boot the machine (you may need the firmware's
boot-device menu the first time), and a menu appears listing your
machines by name:

    install as big
    install as little
    install as tiny

Pick the machine you are standing at. It partitions its own blank
disks, copies the operating system onto them, registers itself with
its firmware, and powers off. Unplug the stick, power it back on,
and it boots from its own disk from then on — the same stick does
every machine, starting with the first leader.

Machines find each other on the addresses you declared, the leaders
form the control plane, and the followers join.

## 6. Talk to your cluster

    ./liken kubeconfig mycluster/identity

This writes `mycluster/identity/kubeconfig`, an administrator
credential. It points at `https://127.0.0.1:16443` (the development
lab's arrangement); edit its `server:` line to your cluster's
endpoint — the `endpoint:` value in your `cluster.yaml`. Then:

    kubectl --kubeconfig mycluster/identity/kubeconfig get nodes

Every machine, Ready. From here it is an ordinary Kubernetes
cluster, plus two liken resources worth meeting:

    kubectl get clusters      what the fleet is, as one document
    kubectl get machines      each machine, as the OS sees it

Configuration changes are edits to those resources. And when a new
liken release comes out, moving the whole fleet to it is two fields
on the Cluster — its `spec.releases` comments explain — with no
rebuilt media and no per-machine work: every machine fetches,
verifies, and proves the new version itself, one at a time.
