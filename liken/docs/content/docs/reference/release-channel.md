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
      sbom.cdx.json            the CycloneDX SBOM: every component in
                               the release
      <file>.sigstore.json     the cosign signature of each file that
                               release.yaml lists, and of release.yaml
      notes.md                 what changed in this release
      coverage.html            the test coverage of the tree it was built from
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

The version tells you only the date. The `components` list of the
release document says what is in the release.

A version always refers to the same bytes. A bad release is never
rebuilt under the same version. The fix is published under the next
serial number.

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
the newest published version. `cluster-operator` polls this document to fill the
AVAILABLE column of `kubectl get clusters`, and `liken fetch
... latest ...` reads it to resolve `latest`.

The channel document is the only file in the channel that changes.
Every other file is published once and never changes, so a machine
can check a release byte for byte against a pinned digest.

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

Read this document when a script needs the list of releases. The
channel is an object storage bucket that does not allow a listing of
its contents, so this file and the front page are the only lists.

No machine reads this document. `cluster-operator` polls only
`channel.yaml`, which stays small however many releases exist. The
digests here are a convenience. A digest that the channel served
proves nothing by itself, so pin the digest in your own Cluster.

## The SBOM

`<version>/sbom.cdx.json` is a CycloneDX SBOM of the release. Give it
to Grype to find the advisories that apply to a release before your
fleet takes it, as [the verify guide](/docs/guides/verify/) shows. It lists:

* each file that the release takes from another project, such as the
  kernel, k3s, and the firmware, with its version, purl, sha256,
  license, and download URL.
* the static programs that buildroot builds, such as `mke2fs` and
  `mount.nfs`, and every package that the build compiled. buildroot
  also builds some libraries that no shipped program links, such as
  libevent, because a recipe depends on them. The build records the
  linker's map of each program, and each package whose code no shipped
  file contains has the scope `excluded`.
* the Go programs that syft finds in the system image, in the
  toolkit, and inside k3s, and the modules that each one links.

The property `liken:source` of each component says where the entry
came from: `manifest`, `buildroot`, or `syft`. The `compositions`
section says which lists are complete. The list of the whole release
is marked incomplete, because syft cannot see what a static C program
links. Such a program appears only when its own build wrote an SBOM.

`release.yaml` lists the SBOM with its digest, like every other
artifact, so each machine downloads the SBOM of the release it runs
into its boot slot with the other files. Older releases have no SBOM.

## Signatures

Each file that `release.yaml` names, and `release.yaml` itself, has a
Sigstore bundle beside it: `<file>.sigstore.json`. The release
workflow signs each file with cosign in keyless mode: Fulcio issues a
certificate for this repository's workflow at the release tag, and
Rekor records the signature. To check a file that you downloaded:

```sh
cosign verify-blob release.yaml --bundle release.yaml.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/liken-sh/liken/\.github/workflows/component-liken\.yaml@refs/tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}$'
```

[`liken verify release`](/docs/reference/cli/#liken-verify-release)
runs the same check on `release.yaml` and on every artifact, with no
cosign, and [Verify a release or an image](/docs/guides/verify/) gives
the steps.

Machines do not read the signatures. A machine trusts a release
through the digest that its Cluster pins, as [the trust
chain](#the-trust-chain) describes. The signatures are for you when you
have no Cluster yet, such as before an install. Releases published
before the workflow signed its files have no bundles.

## The notes

`<version>/notes.md` lists what changed in the release: the commit
subjects since the previous release. The release's page on the
channel and its page on GitHub both show this text. `release.yaml`
does not list the notes, no digest pins them, and no machine reads
them, so notes can be added to an older release later.

## The coverage report

`<version>/coverage.html` is the test coverage of the commit that the
release tag points to: one page that shows the coverage of each
package and marks each line of source as covered or not. [The same
report for the current main](/coverage.html) is on this site.
`release.yaml` does not list the report, and no machine reads it.

Some components of the repository have tests and no manual of their
own, so this site also serves their reports for the current main:
[kubernetes](/coverage/kubernetes.html), the Go module that the
operators share, [media-screen](/coverage/media-screen.html), the
Rust crate that the screen clients share, and
[transect](/coverage/transect.html), the server of a live, read-only
map of a cluster.

## The index pages

Open [releases.liken.sh](https://releases.liken.sh/) in a browser to
read the channel. The front page lists every release, newest first,
and marks the one that `channel.yaml` gives as the latest. Each
release has a page at `https://releases.liken.sh/<version>/` with the
catalog entry to copy, every artifact with its digest and its size,
and the component versions in the release. The mirror at
[sources/](https://releases.liken.sh/sources/) has a page too.

The pages show only what the documents above contain, and no
machine reads them.

## The release page

Every release also has a page on GitHub, under
[liken-sh/liken/releases](https://github.com/liken-sh/liken/releases).
CI creates the page after it publishes the release to the channel.
It shows the digest, the catalog entry to copy, and the notes. The
files themselves are only on the channel, with the license notices and
the source mirror. To hear about new releases, subscribe to the
releases feed on GitHub.

## The trust chain

1. The Cluster's catalog entry pins the sha256 of the exact bytes of
   `release.yaml`. The release's own page on the channel, and its page
   on GitHub, publish the same digest.
2. `release.yaml` pins the sha256 of every artifact.
3. Each machine, and `liken fetch`, verifies every downloaded byte
   against these digests before it uses the bytes.

## Sources

`liken` redistributes binaries under the GPL and the LGPL, so the
channel also serves the source of each such component, at
`sources/<component>/<version>/`. The `LICENSES.md` file in every
release has the license notices. The path uses the component's own
version, not the release's version, because one component version can
be in many releases.
