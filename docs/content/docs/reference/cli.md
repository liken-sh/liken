---
title: The liken command
weight: 30
toc: true
---

# The `liken` command

`liken` is the toolkit for producing and operating a deployment. It
runs on your workstation, and it ships with every release, so
producing a cluster never requires the repository or a build.
Running `liken` with no arguments prints the full usage.

Three terms appear throughout:

* An **identity directory** holds the certificates and join token
  that make a cluster one cluster. Some of the files are private
  keys. Keep the directory out of version control.
* A **deployment layer** is a small archive holding everything about
  the operating system that is yours and not `liken`'s: your
  manifests and your identity. A machine boots the generic image and
  your layer together.
* A **release channel** is a directory any web server can share.
  [The release channel](/docs/reference/release-channel/) describes
  its layout.

## liken new

    liken new <directory>

Starts a deployment. The command asks a few questions and writes a
directory of manifests: `cluster.yaml` and one file per machine, with
comments that teach every field. The other commands build on this
directory.

## liken mint

    liken mint <identity-dir>

Creates a new cluster identity: the certificate authorities and join
token that every machine in one cluster shares.

## liken adopt

    liken adopt <harvest-dir> <identity-dir>

Takes identity files copied off an existing cluster's server and
arranges them as an identity directory. Any k3s cluster's identity
can be adopted. [Adopt an existing k3s
cluster](/docs/guides/adopt/) has the steps.

## liken kubeconfig

    liken kubeconfig <identity-dir>

Writes an administrator kubeconfig: the credential `kubectl` uses to
administer the cluster.

## liken layer

    liken layer <manifests-dir> <identity-dir> <output.cpio>

Packs your cluster's half of the operating system into one small
archive: your cluster and machine manifests, and your identity.

## liken fetch

    liken fetch [-digest sha256:<hex>] <source-url> <version|latest> <channel-dir>

Downloads a published release from a channel into a local channel
directory, and verifies every artifact against the release document.
Pass `latest` to take whatever the channel currently names newest.
`-digest` pins the document itself to a known digest, which closes
the trust chain end to end.

## liken media

    liken media <release-dir> <deployment.cpio> <output.cpio>

Builds a bootable install image from a downloaded release and your
deployment layer. Machines install themselves from it. Use this form
for direct-kernel boots, such as QEMU or PXE.

## liken stick

    liken stick [-console ttyS0] <release-dir> <deployment.cpio> <output.img>

Builds the USB install stick's disk image: one stick for the whole
deployment. Its boot menu gives each machine an install entry and a
wipe-and-reinstall entry, and ends with a hardware report entry that
describes the machine and changes nothing on its disks. Boot it, pick
an entry, and follow the console. `-console` is repeatable, and adds a
`console=` argument that the machines keep permanently.

## liken bundle

    liken bundle [-slot-size 1Gi] <vmlinuz> <liken.sqfs> <boot.cpio> <microcode.cpio> <liken-cli> <systemd-boot.efi> <grub-boot.img> <grub-core.img> <licenses.md> <channel-dir> <version> [component=version ...]

Lays out a release: copies the artifacts into the channel and writes
the `release.yaml` that names each one by its digest. This is the
command the project's own release workflow runs. A deployment does
not need it.

## liken serve

    liken serve <channel-dir> [address]

Shares a release channel over plain HTTP, and logs each request. The
address defaults to `:8017`.

## liken version

    liken version

Prints the toolkit's version.
