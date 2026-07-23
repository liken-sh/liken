---
title: The release channel
weight: 20
toc: true
---

# The release channel

A release channel is a directory that any web server can share. The
public channel lives at
[releases.liken.sh](https://releases.liken.sh/). Machines download
upgrades from it, and `liken fetch` downloads releases from it onto
your workstation.

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
Every field is zero-padded, so plain string comparison sorts versions
in the correct order. The serial starts at `001` and counts up within
the day. Serial `000` never names a published release.

The date is the only thing the version says, on purpose. What shipped
inside a release is recorded in the release document's `components`
section.

A version names exactly one set of bytes, forever. A bad release is
never rebuilt or republished under the same name. The remedy is the
next serial number.

## The release document

`release.yaml` is a `Release` document (`apiVersion:
liken.sh/v1alpha1`). It has two lists:

* `artifacts`: each file in the release, with its `name`, its
  `sha256`, and its `size`.
* `components`: the upstream projects inside, each with its `name`
  and `version` — the kernel, k3s, and the rest.

## The channel document

`channel.yaml` is a `Channel` document. Its `latest` field names the
newest published version. The cluster polls this document to fill
the AVAILABLE column of `kubectl get clusters`, and `liken fetch
... latest ...` reads it to resolve `latest`.

## The release page

Every release also has a page on GitHub, under
[liken-sh/liken/releases](https://github.com/liken-sh/liken/releases).
CI creates the page after it publishes the release to the channel.
The page is the announcement, not the distribution. It carries the
digest, the catalog entry ready to paste, and the changes since the
last release. The binaries stay on the channel, where the license
notices and the source mirror travel with them. The page's feed is a
way to hear about new releases.

## The trust chain

The chain has three links:

1. The Cluster's catalog entry pins the sha256 of `release.yaml`'s
   exact bytes. The release's page on GitHub publishes the same
   digest.
2. `release.yaml` pins the sha256 of every artifact.
3. Each machine, and `liken fetch`, verifies every downloaded byte
   against those digests before anything is used.

## Sources

`liken` redistributes GPL- and LGPL-licensed binaries, so the channel
also serves each such component's source, at
`sources/<component>/<version>/`. `LICENSES.md` inside every release
carries the notices. The paths are keyed by the component's own
version, not the release's, because one component version can appear
in many releases.
