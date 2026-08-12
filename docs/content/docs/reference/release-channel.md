---
title: The release channel
weight: 20
toc: true
---

# The release channel

A release channel is a directory that any web server can share. The
public channel is at [releases.liken.sh](https://releases.liken.sh/).
Machines download upgrades from it, and
[`liken fetch`](/docs/reference/cli/#liken-fetch) downloads releases
from it to your workstation. [Upgrade the
fleet](/docs/guides/upgrade/) gives the steps that use it.

## Layout

    channel.yaml               the channel document: the latest version
    versions.yaml              the versions document: every release, by digest
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
      notes.md                 what changed in this release
    sources/                   source mirrors for GPL and LGPL components,
      <component>/<version>/   keyed by the component's own version
    index.html                 the index pages, one at the root, one in
                               each release, and one in sources/
    favicon.ico                the icon browsers request

## Versions

A version is a calendar date and a serial number: `2026.07.20-001`.
Every field is zero-padded to a fixed width, so a plain string
comparison puts versions in the correct order. The serial starts at `001` and
increases during the day. No published release uses serial `000`.

The version gives the date only. The `components` section of the
release document records what is in the release.

A version always refers to the same bytes. If a release is bad,
nobody builds it again or publishes it again with the same name.
Publish the next serial number instead.

## The release document

`release.yaml` is a `Release` document (`apiVersion:
liken.sh/v1alpha1`). It has two lists:

* `artifacts`: each file in the release, with its `name`, its
  `sha256`, and its `size`.
* `components`: the upstream projects in the release, each with its
  `name` and `version`. These are the kernel, k3s, and the other
  components.

## The channel document

`channel.yaml` is a `Channel` document. Its `latest` field records
the newest published version. The cluster polls this document to fill the
AVAILABLE column of `kubectl get clusters`, and `liken fetch
... latest ...` reads it to resolve `latest`.

The channel document is the only object in the channel that changes.
Every other object is published one time and never changes, which is
why a machine can verify a release byte for byte against a pinned
digest.

## The versions document

`versions.yaml` is a `Versions` document. It lists every release that
the channel holds, newest first, each with the digest of its release
document:

```yaml
apiVersion: liken.sh/v1alpha1
kind: Versions
metadata:
  name: liken
latest: 2026.07.25-002
releases:
  - version: 2026.07.25-002
    digest: sha256:f5a46b8b08405d6b79c4792c896089e6b3cbbb4ce1951e84a6a36656520cd616
```

Each entry has the shape of a catalog entry, so you can copy one
straight into your cluster's
[`spec.releases.catalog`](/docs/reference/cluster/#specreleasescatalog).

Read this document when you want the whole list in one request. The
storage refuses to list itself, so this file is the only way to learn
which versions exist without opening the front page.

No machine reads this document. A cluster polls `channel.yaml`, which
stays one small file however many releases exist. The digests here are
a convenience, not an authority: a digest that the channel served
vouches for nothing by itself, which is why you pin the digest in your
own Cluster.

## The notes

`<version>/notes.md` lists what changed in the release: the commit
subjects since the release before it. The release's page on the
channel shows the list, and the body of the release's page on GitHub
wraps the same text. No digest pins the notes, `release.yaml` does
not list them, and no machine reads them. They are announcement
prose, in the same trust class as the index pages. That is also what
lets a release published before notes existed gain them later.

## The index pages

Open [releases.liken.sh](https://releases.liken.sh/) in a browser to
read the channel. The front page lists every release, newest first,
and marks the one that `channel.yaml` records as the latest. Each
release has a page
at `https://releases.liken.sh/<version>/`. It gives the catalog entry
to copy, every artifact with its digest and its size, and the
component versions in the release. The mirror at
[sources/](https://releases.liken.sh/sources/) has a page too.

These pages carry no information of their own. Each page is a view of
the documents above, and no machine reads a page. A machine reads
`channel.yaml` and `release.yaml`, and it verifies both.

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
   `release.yaml`. The release's own page on the channel, and its page
   on GitHub, publish the same digest.
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
