---
name: verify
description: "Check that a liken release or a liken image is the one that liken's CI published, before you install it, adopt it into a Cluster's catalog, or deploy it, with liken verify release, liken verify image, and liken sbom. Read the packages of a release's or an image's SBOM, give the SBOM to a vulnerability scanner, and learn what each failure of a check means. Use before an install or an upgrade, before a catalog edit, when auditing what a fleet runs, or when a verify command fails."
---

This skill is the guide at https://liken.sh/docs/guides/verify/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Verify a release or an image

At the end of this guide, you know that a release or an image came
from `liken`'s CI, and you have its SBOM.

`liken`'s CI signs every file of an OS release and every image that
it publishes, with Sigstore in keyless mode. The certificate of each
signature records the GitHub workflow that ran the publish and the tag
or branch it ran at, and Sigstore's public log, Rekor, records each
signature. So a valid signature proves that this repository's workflow
published the bytes you downloaded, and that no person, fork, or pull
request made them.

[`liken verify`](https://liken.sh/docs/reference/cli/#liken-verify-release) checks
these signatures itself, so you do not need cosign. Sigstore's keys
and certificates are compiled into the toolkit, and the check reads no key from
the network.

You need:

* The `liken` toolkit, from step 1 of the
  [install](https://liken.sh/docs/guides/install/#1-download-a-release).
* For a release, its version and the digest of its `release.yaml`.
  The release's page on GitHub shows both, as a catalog entry.

## 1. Verify a release

Give the version, and pin `release.yaml` to the digest from the
catalog entry:

    liken verify release -digest sha256:<hex> 2026.11.01-001

The command checks `release.yaml` against the digest, then checks its
Sigstore bundle, `release.yaml.sigstore.json`. Then it downloads each
artifact that `release.yaml` names, checks its size and its sha256
against `release.yaml`, and checks the artifact's own bundle. It
prints one line for each file, and a summary of the SBOM at the end:

    ok  release.yaml  signed by https://github.com/liken-sh/liken/.github/workflows/component-liken.yaml@refs/tags/2026.11.01-001
    ok  vmlinuz
    ok  liken.sqfs
    ...
    ok  sbom.cdx.json
    sbom.cdx.json: 744 components: 64 buildroot, 23 manifest, 657 syft

The command downloads the whole release, so it takes as long as
[`liken fetch`](https://liken.sh/docs/reference/cli/#liken-fetch). It hashes each
artifact as it downloads and writes nothing to disk.

The signature must come from `component-liken.yaml` at a release tag,
because the OS publishes from release tags only. Without `-digest`,
the command still checks every signature, but it does not prove that
the release is the one that your catalog entry pins. A Cluster trusts
a release only through that digest, so check with `-digest` before you
add the entry to
[`spec.releases.catalog`](https://liken.sh/docs/reference/cluster/#specreleasescatalog).

`-source` reads a channel other than `https://releases.liken.sh`,
such as a mirror. The signatures are the same on every copy.

## 2. Verify an image

Give the image's reference with its tag or its digest:

    liken verify image ghcr.io/liken-sh/bluetoothd:2026.11.01-001

The command resolves the tag to the digest of the image's index,
checks the cosign signature of that digest, and reads the index's
attestations for each platform: the SPDX SBOM and the SLSA
provenance of the build.

    ok  ghcr.io/liken-sh/bluetoothd@sha256:abbc9736...  signed by https://github.com/liken-sh/liken/.github/workflows/component-bluetooth-operator.yaml@refs/tags/2026.11.01-001
    ok  linux/amd64  SBOM of 21 packages, provenance https://slsa.dev/provenance/v1

The signature must come from a component's workflow,
`component-<name>.yaml`, at `main` or at a release tag. A development
build is signed at `main`, and a release at its tag. Pin the digest
that the first line prints in your manifests, so that a later push to
the tag cannot change what you deploy.

The image's labels and annotations give its name, a one-line
description, its version, the commit it was built from, the time of
that commit, and the address of its manual. The signature covers them:
the signed index contains the annotations and references the image's
config, which contains the labels. Read them through the digest that
the first line prints:

    docker buildx imagetools inspect ghcr.io/liken-sh/bluetoothd@sha256:abbc9736... --format '{{ json .Image.Config.Labels }}'
    docker buildx imagetools inspect --raw ghcr.io/liken-sh/bluetoothd@sha256:abbc9736... | jq .annotations

`org.opencontainers.image.revision` is the commit, and
`org.opencontainers.image.licenses` is the license of `liken`'s own
code in the image. The SBOM lists the license of every other package.

## 3. Read an SBOM

[`liken sbom`](https://liken.sh/docs/reference/cli/#liken-sbom) checks the signature
first, the same way as `liken verify`, and then prints the name,
version, and package URL of each package. An argument in the form of a
version, such as `2026.11.01-001`, is a release. Any other argument is
an image reference:

    liken sbom 2026.11.01-001
    liken sbom ghcr.io/liken-sh/bluetoothd:2026.11.01-001

For a release, the command downloads only `release.yaml` and the SBOM.
The property `liken:source` of each component of a release's SBOM
records where it came from, as [the release
channel](https://liken.sh/docs/reference/release-channel/#the-sbom) describes.

`-json` prints the document itself: CycloneDX for a release, and SPDX
for an image. Save it and give it to a vulnerability scanner to find the advisories
that apply before you adopt a release or deploy an image.

Scan a release with Grype. No package database records the kernel,
bootloaders, microcode, k3s, or Flux of a release, so the SBOM lists
each of them with its CPE from NVD. Grype matches a component by its
CPE, and Trivy does not, so Trivy finds only the Go modules of a
release:

    liken sbom -json 2026.11.01-001 >liken-2026.11.01-001.cdx.json
    GRYPE_MATCH_DPKG_USING_CPES=true grype sbom:liken-2026.11.01-001.cdx.json --only-fixed

`GRYPE_MATCH_DPKG_USING_CPES=true` lets Grype match GRUB and
systemd-boot, which the SBOM names as packages of Ubuntu's archive, by
their CPEs. `--only-fixed` keeps only the findings that a newer version
fixes. Without it, the report includes many old kernel records that
NVD lists with no upper version bound, and each one matches every
kernel. Grype ignores a component's scope, so it also reports the
compiler and the host tools of the build. The SBOM gives them the
scope `excluded` because the release does not ship them. `liken`'s CI
runs the same scan on each release without those components, and the
repository's
[code scanning alerts](https://github.com/liken-sh/liken/security/code-scanning)
show the findings of the newest release under the category
`grype/liken-release`, and the findings of the newest commit on `main`
under `grype/liken`.

Either scanner reads an image's SBOM:

    liken sbom -json ghcr.io/liken-sh/bluetoothd:2026.11.01-001 >bluetoothd.spdx.json
    grype sbom:bluetoothd.spdx.json

An image for several platforms has one SBOM for each platform.
`-json` prints the documents one after the other, and the table
prints a `# <platform>` line before each platform's packages.

## When a check fails

Each command stops at the first file that fails, and the message
includes the file's name. Do not install, add to a catalog, or deploy
anything that failed a check.

| Message | Meaning |
|---|---|
| `release.yaml: the digest is sha256:..., and -digest pins sha256:...` | The channel served a different `release.yaml` than the one that your catalog entry pins. Check the digest that you copied, and the `-source`. |
| `artifact <file> has sha256 ..., want ...` | The bytes differ from the digest in `release.yaml`: the download was changed or damaged. Run the command again. If it fails the same way, the channel serves a wrong file. |
| `artifact <file> has N bytes, want M` | The download stopped early, or the server sent more bytes than `release.yaml` lists. Run the command again. |
| `<file>: verifying the signature over sha256:...: failed to verify certificate identity` | A valid Sigstore signature covers the bytes, but another workflow, another repository, or another ref signed them. A fork, a branch, or a pull request is never a `liken` publish. |
| `<file>: verifying the signature over sha256:...: failed to verify signature` | The bundle does not sign these bytes. It is the bundle of another file, or the file changed after it was signed. |
| `<file>: fetching ...: the server returned 404 Not Found` | The channel has no such file. A release published before the workflow signed its files has no `.sigstore.json` bundles, and `liken verify` cannot check it. |
| `release.yaml is the document of version A, not B` | The channel returned the signed document of another version for the version you asked for. |
| `sbom.cdx.json: the SBOM does not follow CycloneDX 1.7` | The SBOM is signed but is not a valid CycloneDX document. Report it. |
| `... has no Sigstore signature` | Nothing signed the image's index. A local build, or an image that no `liken` publish job pushed, has none. |
| `no signature of ... verifies` | Each signature of the index failed. The message lists the reason for each, in the forms above. |
| `... is a application/vnd.oci.image.manifest.v1+json, and a published image is an index` | The reference names one platform's image, not the index that CI signed. Give the tag, or the digest of the index. |
| `the linux/amd64 image has no SPDX SBOM`, or `has no SLSA provenance` | The index lacks an attestation that every `liken` publish attaches. |
