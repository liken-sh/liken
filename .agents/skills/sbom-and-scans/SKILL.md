---
name: sbom-and-scans
description: How the liken repository records and checks what it publishes - the SBOM and the SLSA provenance that buildx attaches to each image, the CycloneDX record that an image carries for the files syft cannot find, the keyless cosign signature of each image and of each file of an OS release, the OS release's sbom.cdx.json and how it is merged, and the Trivy and grype vulnerability scans with the scheduled scan. Use when verifying a published image or a release file, when reading or changing an SBOM, a signature, or a vulnerability scan, when a finding in the Security tab needs an answer, and when rehearsing the signing of a lab release.
---

# SBOMs, signatures, and vulnerability scans

Every image and every OS release that CI publishes carries a list of
what it holds and a signature of the workflow that built it, so a
person who pulls one can check both without the tree it was built
from. The `releases` skill holds the version scheme, what a tag
publishes, and the labels of an image. The `pins` skill holds the
`pin.toml` manifest that the records here read.

## Image attestations and signatures

Every image that a publish job pushes, in a development build, a
release, or a pinned component's tag, carries two buildx attestations:
an SPDX SBOM, and the build's SLSA provenance in max mode, which
records the build arguments, the named contexts, the Dockerfile, and
the digest of each base image and download. The commit is in the
image's `org.opencontainers.image.revision` label. buildx stores both
attestations in the image's index on ghcr.io, as a manifest of the
platform `unknown/unknown` beside the image's own manifest. The SBOM
comes from buildx's syft scanner, which `toolchains/images.toml` pins
as `buildkit-syft-scanner`, so the program that writes each SBOM
changes only with a commit. The BuildKit daemon that builds each
image is pinned the same way, as `buildkit`: each generated
`docker/setup-buildx-action` step names its digest in `driver-opts`,
because with no image the action and buildx start BuildKit's default
image, whose tag moves to each BuildKit release. `cd ci && go run . pins -root ..` fails a step that names
no image or another one. `ci/sign.go` sets both attestations on the
one target that the publish pushes, so a base that the build needs as
a context carries none. The bake file carries none either, because the
images job and a workstation load the image into Docker.

syft lists what a program or a package database records about itself:
a Go binary's build info, the crates that cargo-auditable records,
and the dpkg records of a closure. It finds nothing in a file that a
Dockerfile fetches by its checksum, or in a static program that
buildroot built. So an image that ships such files carries a CycloneDX
record of them at `/usr/share/doc/liken/<image>.cdx.json`, which
`image-sbom` from `toolchains/manifest` writes during the build from
the pin's `pin.toml`, or from buildroot's SBOM for the bluetoothd
image. syft copies each record's components into the image's SBOM,
with the purl, the CPE, and the license of the record, and gives the
record's path as their `sourceInfo`. An image that copies another
image's tree, such as `COPY --from=trust / /`, copies its record too.
"Pins and downloads" in the `pins` skill gives the rule.

After the push, the publish job runs cosign, from the image that
`toolchains/images.toml` pins, and signs the digest of the index
under each name of the image. The signature is keyless: the job holds
`id-token: write`, Fulcio certifies the component's workflow at its
ref as the signer, and Rekor records the signature. cosign pushes the
signature to the image's own ghcr.io repository, as an OCI referrer
of the digest, or under the tag `sha256-<digest>` on a registry that
has no referrers API. Only the publish job of a component with images
and the OS's publish job hold `id-token: write`. A dry run builds the
same attestations and signs nothing.

A signature that fails fails the publish, before `:latest` moves and
before the deploy artifact names the image. The rerun finds the
version published, and signs it again by its tag, which cosign
resolves to the digest that the failed run pushed.

To verify an image, resolve its tag to a digest once, and read
everything by that digest:

```sh
image=ghcr.io/liken-sh/git-csi-driver
digest="$(docker buildx imagetools inspect "$image:2026.11.01-001" --format '{{ .Manifest.Digest }}')"
cosign verify "$image@$digest" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/liken-sh/liken/\.github/workflows/component-[a-z0-9-]+\.yaml@refs/(heads/main|tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3})$'
docker buildx imagetools inspect "$image@$digest" --format '{{ json .SBOM }}'
docker buildx imagetools inspect "$image@$digest" --format '{{ json .Provenance }}'
```

`liken verify image "$image:2026.11.01-001"` runs the same checks
in-process, with sigstore-go and the trusted root pinned in
`liken/verify/`: it resolves the tag to the index's digest, verifies
the signature against the same identity, and reports the signer and
the package count of each platform's SBOM. `liken sbom <reference>`
prints the SBOM's packages after the same check, and `-json` prints
the SPDX document. The commands above stay the reference for what
the check does.

To list the packages of the SBOM that came from a record, with the
purl and the CPE of each:

```sh
docker buildx imagetools inspect "$image@$digest" --format '{{ json .SBOM.SPDX }}' |
  jq -r '.packages[] | select((.sourceInfo // "") | test("/usr/share/doc/liken/.*[.]cdx[.]json"))
    | [.name, .versionInfo, (.externalRefs[]?.referenceLocator)] | join(" ")'
```

`cosign verify` checks that Fulcio issued the certificate to a
workflow of this repository from GitHub's OIDC issuer, that the
workflow ran at `main` or at a release tag, that the signature covers
the digest, and that Rekor holds it. A branch or a pull request never
publishes, and the identity refuses a signature from any other ref.
To require one component, put its workflow in the identity, as in
`component-git-csi-driver\.yaml`. The SBOM and the provenance carry no
signature of their own. The index names each of them by its digest,
so they are the ones that the signature covers when they are read
through the digest that `cosign verify` checked. The same holds for
the labels and the annotations: the annotations of the index are in
the bytes that the digest names, the index names each image manifest
by its digest, and each manifest names its config by its digest.

## The OS release's SBOM and signatures

Each OS release carries `sbom.cdx.json`, a CycloneDX SBOM, at
`https://releases.liken.sh/<version>/sbom.cdx.json`. `release.yaml`
names it by its digest like every other artifact, so it is on each
machine's boot slot too. `make release` writes it before the bundle,
with `liken/releases/sbom/scan.sh`, from three sources:

* each `pin.toml` of a pin that the release ships: the kernel, k3s,
  the firmware, and the rest, each with its purl, version, sha256,
  license, and download URL. An entry with `scope = "excluded"`,
  which only the build reads, keeps that scope;
* buildroot's SBOM, `liken/userland/dist/sbom.cdx.json`, for the
  static programs and every package they link, host packages with
  the scope `excluded`;
* syft, pinned in `toolchains/images.toml`, over the root filesystem
  that the image build staged, each image archive in it, the toolkit,
  and the programs that k3s writes out of its own binary. This is
  where liken's own Go programs and their modules, and k3s's
  containerd, runc, and CNI plugins, come from.

`liken/releases/sbom` merges them. A program that syft finds at a
path that `scan.sh` claims for a manifest or a buildroot package,
such as `/bin/k3s`, keeps the manifest's or buildroot's component,
and the modules it links depend on that component. The subject,
`metadata.component`, is the release at its CalVer version. The
`compositions` state `complete` for buildroot's packages and for each
of liken's own Go programs, and `incomplete` for the release as a
whole and for k3s, because syft identifies only programs that record
what they contain. The merge checks the document against the
CycloneDX schema in `liken/releases/sbom/schema/` before it writes
the file. The lab's working-tree channel carries an SBOM of the
manifests and buildroot alone, with no syft scan.

Count the components by source, and read one:

```sh
sbom=releases/dist/<version>/sbom.cdx.json
jq -r '.components[] | .properties[] | select(.name == "liken:source") | .value' "$sbom" | sort | uniq -c
jq '.components[] | select(.name == "k3s")' "$sbom"
jq '.compositions' "$sbom"
```

The publish job signs `release.yaml` and every file that it names,
keyless, with the cosign that `toolchains/images.toml` pins, through
`liken/releases/sign.sh`, before `publish.sh` uploads the directory.
Fulcio certifies `component-liken.yaml` at the release tag, Rekor
records each signature, and the Sigstore bundle of each file is at
`<file>.sigstore.json` beside it. To verify a downloaded file:

```sh
v=2026.11.01-001
curl -fsSLO "https://releases.liken.sh/$v/release.yaml"
curl -fsSLO "https://releases.liken.sh/$v/release.yaml.sigstore.json"
cosign verify-blob release.yaml --bundle release.yaml.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/liken-sh/liken/\.github/workflows/component-liken\.yaml@refs/tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}$'
```

The OS publishes from release tags alone, so the identity names no
branch. `liken verify release -digest sha256:<hex> <version>`
runs this check on `release.yaml` and on every artifact it names, and
checks each artifact's digest and the SBOM's schema too.
`liken sbom <version>` checks `release.yaml` and the SBOM the same way
and prints the SBOM's packages. `liken/verify/` holds both checks,
and `liken/verify/latest.sh` reports and bumps the Sigstore trusted
root that they read. A verified `release.yaml` also vouches for each artifact by
its digest, as it does for a machine.

To rehearse the signing on a lab release, sign with a throwaway key.
The script then writes nothing to the public Rekor, so the verify
skips the log:

```sh
cosign=$(awk -F' = ' '$1 == "cosign" { gsub(/"/, "", $2); print $2 }' ../toolchains/images.toml)
key=$(mktemp -d)
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -e COSIGN_PASSWORD= -v "$key:/k" -w /k "$cosign" generate-key-pair
COSIGN_PASSWORD= releases/sign.sh <version> "$key/cosign.key"
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$key:/k:ro" -v "$PWD/releases/dist/<version>:/r:ro" -w /r "$cosign" \
  verify-blob --key /k/cosign.pub --insecure-ignore-tlog --bundle sbom.cdx.json.sigstore.json sbom.cdx.json
rm -rf "$key"
```

## Vulnerability scans

Every finding is in the repository's Security tab, under code
scanning, and no finding fails a run.

Code scanning keeps each upload under the ref that the upload names,
and the Security tab's view of the default branch reads only the
uploads on `main`. So every upload of a scan names `main` when its run
is on a release tag, with the run's commit. `ci/templates/sarif.tmpl`
holds the two inputs.

* **Each image.** The publish job of a component runs Trivy, pinned in
  `ci/trivy.go`, on each image that it pushes, and uploads the
  findings that have a fixed version under the category
  `trivy/<image>`.
* **The OS.** The OS publishes no image, and no package
  database records its kernel, bootloaders, microcode, k3s, or Flux. Each of
  those `pin.toml` entries states the CPE that NVD lists the upstream
  under, and `liken/releases/sbom/vulnerabilities.sh` runs grype,
  pinned in `toolchains/images.toml`, over an SBOM of the OS. Trivy matches no component by its CPE, so this scan
  alone runs grype. The script leaves out each component with the
  scope `excluded`, keeps each finding that has a fixed version, and
  reads `grype.yaml` beside it, which names each finding that is false
  for every release with the reason.
* **Each build of the OS.** The OS's `build` job runs
  `make vulnerabilities` after the smoke drills. The target writes the
  SBOM of what the commit built, with the syft scan that a release
  runs, and scans it. The job's entry in `liken/package.toml` states
  `grype = true`, which makes the generated job upload the file
  `grype/liken.sarif` that its command writes, and `ci` refuses a job
  that states the field and writes no such file. The upload goes under
  the category `grype/liken`, so that category holds the findings of the tree at
  `main`, and a finding that a bump can clear shows before a release
  ships it.
* **Each published release.** The step "scan the release's SBOM" of
  the OS's publish job uploads under the category
  `grype/liken-release`. The findings of the tree and of the newest
  release have a category each, because code scanning keeps one set
  of results for a category, and an upload replaces the set before
  it.
* **Every third day.** `.github/workflows/scan.yaml`, which `ci/`
  generates from `ci/templates/scan.yaml.tmpl`, runs at 04:43 UTC on
  the 1st, 4th, and each third day of a month to the 31st, and on a
  dispatch. `go run . scans` in `ci/` lists every published image at
  the version that a cluster pulls and the newest OS release on the
  channel. The `trivy` job scans each image, and the `liken` job
  fetches the release's SBOM with `liken sbom -json`, which checks its
  signature, scans it with grype, and uploads under
  `grype/liken-release`. The timer is a backstop: a
  vulnerability that is recorded after a publish reaches no alert
  until a later scan, and the vulnerability databases publish no event
  that a workflow can subscribe to.
* **A change to the Trivy configuration.** A push to `main` that
  changes `ci/trivy.go` or `ci/templates/trivy.tmpl` scans every
  published image again. `Planner.PublishedImages` in `ci/rescan.go`
  gives the images and the versions for this scan and for the
  scheduled one.

To scan on a workstation, with docker and `jq`: the tree, after
`make all`, and then a release.

```sh
cd liken
make vulnerabilities
go run ./cli sbom -json 2026.11.10-001 >sbom.cdx.json
releases/sbom/vulnerabilities.sh sbom.cdx.json
```

The scan does not match `linux-firmware`, which NVD does not list,
or the iptables programs of `k3s-root-xtables`, whose entry states
k3s-root's version.
