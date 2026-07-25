---
title: The release channel
weight: 20
toc: true
---

# The release channel

A release channel is a directory that any web server can share. The
public channel is at [releases.liken.sh](https://releases.liken.sh/).
Machines download upgrades from it, and `liken fetch` downloads
releases from it to your workstation.

## Layout

    channel.yaml               the channel document: names the latest version
    <version>/                 one directory per release
      release.yaml             the release document: every artifact, by digest
      vmlinuz                  the Linux kernel
      liken.sqfs               the operating system: a read-only squashfs
                               image a machine mounts as its root
      boot.cpio                the small initramfs: init and the early
                               boot's kernel modules
      microcode.cpio           CPU microcode, loaded ahead of everything else
      liken                    the toolkit
      systemd-bootx64.efi      the install stick's boot menu, for UEFI
      grub-boot.img            the BIOS boot loader's first stage
      grub-core.img            the BIOS boot loader's second stage
      LICENSES.md              third-party license notices
    sources/                   source mirrors for GPL and LGPL components,
      <component>/<version>/   keyed by the component's own version

## Versions

A version is a calendar date and a serial number: `2026.07.20-001`.
Every field is zero-padded to a fixed width, so a plain string
comparison puts versions in the correct order. The serial starts at `001` and
increases during the day. Serial `000` never names a published
release.

The version gives the date only. The `components` section of the
release document records what is in the release.

A version always names the same bytes. If a release is bad, nobody
builds it again or publishes it again with the same name. Publish the
next serial number instead.

## The release document

`release.yaml` is a `Release` document (`apiVersion:
liken.sh/v1alpha1`). It has two lists:

* `artifacts`: each file in the release, with its `name`, its
  `sha256`, and its `size`.
* `components`: the upstream projects in the release, each with its
  `name` and `version`. These are the kernel, k3s, and the other
  components.

## The channel document

`channel.yaml` is a `Channel` document. Its `latest` field names the
newest published version. The cluster polls this document to fill the
AVAILABLE column of `kubectl get clusters`, and `liken fetch
... latest ...` reads it to resolve `latest`.

## The release page

Every release also has a page on GitHub, under
[liken-sh/liken/releases](https://github.com/liken-sh/liken/releases).
CI makes the page after it publishes the release to the channel. The
page is the announcement, not the distribution. It gives the digest,
the catalog entry that you can copy, and the changes after the last
release. The binaries stay on the channel, with the license notices
and the source mirror. To hear about new releases, use the page's
feed.

## The trust chain

The chain has three links:

1. The Cluster's catalog entry pins the sha256 of the exact bytes of
   `release.yaml`. The release's page on GitHub publishes the same
   digest.
2. `release.yaml` pins the sha256 of every artifact.
3. Each machine, and `liken fetch`, verifies every downloaded byte
   against these digests before it uses the bytes.

## Sources

`liken` redistributes binaries with the GPL license and the LGPL
license, so the channel also serves the source of each such
component, at `sources/<component>/<version>/`. The `LICENSES.md`
file in every release gives the notices. The paths use the
component's own version, not the release's version, because one
component version can be in many releases.
