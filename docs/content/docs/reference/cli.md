---
title: The liken command
weight: 30
toc: true
---

# The `liken` command

`liken` is the toolkit that you use to make and to operate a
deployment. It runs on your workstation, and every release includes
it. You do not need the repository or a build to make a cluster. To
print the full usage, run `liken` with no arguments.

Three terms occur on this page:

* An **identity directory** holds the certificates and the join token
  that all the machines in one cluster share. Some of the files are
  private keys. Keep the directory out of version control.
* A **deployment layer** is a small archive that holds the parts of
  the operating system that are yours and not `liken`'s: your
  manifests and your identity. A machine boots the generic image and
  your layer together.
* A **release channel** is a directory that any web server can share.
  [The release channel](/docs/reference/release-channel/) describes
  its layout.

## liken new

    liken new <directory>

Starts a deployment. The command asks a few questions and writes a
directory of manifests: `cluster.yaml` and one file for each machine.
The comments in the files describe every field. The other commands
use this directory.

## liken mint

    liken mint <identity-dir>

Makes a new cluster identity: the certificate authorities and the
join token that all the machines in one cluster share.

## liken adopt

    liken adopt <harvest-dir> <identity-dir>

Takes identity files that you copied from the server of an existing
cluster, and arranges them as an identity directory. You can adopt
the identity of any k3s cluster. [Adopt an existing k3s
cluster](/docs/guides/adopt/) gives the steps.

## liken kubeconfig

    liken kubeconfig <identity-dir>

Writes an administrator kubeconfig: the credential that `kubectl`
uses to administer the cluster.

## liken layer

    liken layer <manifests-dir> <identity-dir> <output.cpio>

Packs your cluster's part of the operating system into one small
archive: your cluster manifest, your machine manifests, and your
identity.

## liken fetch

    liken fetch [-digest sha256:<hex>] <source-url> <version|latest> <channel-dir>

Downloads a published release from a channel into a local channel
directory, and verifies every artifact against the release document.
To take the version that the channel names as the newest, give
`latest`. `-digest` pins the release document to a known digest,
which completes the trust chain.

## liken media

    liken media <release-dir> <deployment.cpio> <output.cpio>

Builds a bootable install image from a downloaded release and your
deployment layer. Machines install themselves from it. Use this form
for direct-kernel boots, for example QEMU or PXE.

## liken stick

    liken stick [-console ttyS0] <release-dir> <deployment.cpio> <output.img>

Builds the disk image for the USB install stick: one stick for the
full deployment. Boot the stick, select an entry, and obey the
console.

The menu holds two entries for each machine in the deployment, in the
order of the machine names, and one entry for the stick itself:

    install as big
    wipe and reinstall as big
    install as little
    wipe and reinstall as little
    liken hardware report

`install as <name>` uses blank disks only. `wipe and reinstall as
<name>` erases every disk that the manifest of that machine declares,
then installs. The report entry is last in the list because it
applies to no machine: it describes the hardware of the machine in
front of it and changes no disk. The menu has no time limit, because
every entry writes to a disk or asks for a person. A machine that
stays at the menu does nothing until a person selects an entry.

`-console` is repeatable. It adds a `console=` argument that the
machines keep permanently. Use it to install a machine that has no
screen: the menu and all the messages also go to that port.

## liken bundle

    liken bundle [-slot-size 1Gi] <vmlinuz> <liken.sqfs> <boot.cpio> <microcode.cpio> <liken-cli> <systemd-boot.efi> <grub-boot.img> <grub-core.img> <licenses.md> <channel-dir> <version> [component=version ...]

Lays out a release: it copies the artifacts into the channel and
writes the `release.yaml` that names each one by its digest. The
project's own release workflow runs this command. A deployment does
not need it.

## liken serve

    liken serve <channel-dir> [address]

Shares a release channel over plain HTTP, and records each request in
a log. The address defaults to `:8017`.

## liken version

    liken version

Prints the version of the toolkit.
