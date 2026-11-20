---
name: sbom-and-scans
description: How the liken repository records and checks what it publishes - the SBOM and the SLSA provenance that buildx attaches to each image, the CycloneDX record that an image includes for the files syft cannot find, the keyless cosign signature of each image and of each file of an OS release, the OS release's sbom.cdx.json and how it is merged, and the Trivy and grype vulnerability scans with the scheduled scan. Use when verifying a published image or a release file, when reading or changing an SBOM, a signature, a SARIF upload, or a vulnerability scan, when a CVE, a code scanning alert, or a finding in the Security tab needs an answer or should be cleared, and when rehearsing the signing of a lab release.
---

# SBOMs, signatures, and vulnerability scans

Every image and every OS release that CI publishes comes with a list
of what it contains and a signature from the workflow that built it.
A person who pulls one can check both without the tree it was built
from. The version scheme, what a tag publishes, and the labels of an
image are in the `releases` skill. The `pin.toml` manifest that these
records are made from is in the `pins` skill.

## Which section to read

* **Verify an image, or read its SBOM:** "Image attestations and
  signatures".
* **Verify a file of an OS release, or read the release's SBOM:** "The
  OS release's SBOM and signatures".
* **Rehearse the signing of a lab release:** the end of "The OS
  release's SBOM and signatures".
* **Open alerts, a CVE, or a change to a scan:** "Vulnerability scans".
* **Decide what to do with one alert, or clear it:** "Answer a
  finding".

## Image attestations and signatures

Every image that a publish job pushes, in a development build, a
release, or a pinned component's tag, has two buildx attestations. One
is an SPDX SBOM. The other is the build's SLSA provenance in max mode,
which records the build arguments, the named contexts, the Dockerfile,
and the digest of each base image and download. The commit is in the
image's `org.opencontainers.image.revision` label. buildx stores both
attestations in the image's index on ghcr.io, as a manifest with the
platform `unknown/unknown` next to the image's own manifest.

buildx's syft scanner writes the SBOM. `toolchains/images.toml` pins
the scanner as `buildkit-syft-scanner`, so the program that writes
each SBOM changes only with a commit. The BuildKit daemon that builds
each image is pinned the same way, as `buildkit`. Each generated
`docker/setup-buildx-action` step sets the daemon's digest in
`driver-opts`. With no image set there, the action and buildx start
BuildKit's default image, whose tag moves to each BuildKit release.
`cd ci && go run . pins -root ..` fails on a step that sets no image
or a different one. `ci/sign.go` sets both attestations only on the
target that the publish pushes, so a base that the build uses as a
context has none. The bake file sets none either, because the images
job and a workstation load the image into Docker.

syft lists what a program or a package database records about itself:
a Go binary's build info, the crates that cargo-auditable records, and
the dpkg records that a closure image writes for the Debian or Ubuntu
packages it copies files from. syft finds nothing in a file that a Dockerfile
fetches by its checksum, or in a static program that buildroot built.
So an image that ships such files includes a CycloneDX record of them
at `/usr/share/doc/liken/<image>.cdx.json`. `image-sbom` from
`toolchains/manifest` writes this record during the build, from the
pin's `pin.toml`, or from buildroot's SBOM for the bluetoothd image.
syft copies each record's components into the image's SBOM, with the
purl, the CPE, and the license from the record. It sets the record's
path as their `sourceInfo`. An image that copies another image's tree,
such as with `COPY --from=trust / /`, copies that image's record too.
The `pins` skill describes the record and `image-sbom` under "The
manifest".

After the push, the publish job runs cosign, from the image that
`toolchains/images.toml` pins. cosign signs the digest of the index
under each name of the image. The signature is keyless: the job has
the permission `id-token: write`, Fulcio certifies the component's
workflow at its ref as the signer, and Rekor records the signature.
cosign pushes the signature to the image's own ghcr.io repository, as
an OCI referrer of the digest. On a registry with no referrers API, it
uses the tag `sha256-<digest>` instead. Only the publish job of a
component with images and the OS's publish job have
`id-token: write`. A dry run builds the same attestations and signs
nothing.

If the signing fails, the publish fails. This happens before `:latest`
moves and before the deploy artifact refers to the image. The rerun
finds the version already published and signs it again by its tag.
cosign resolves the tag to the digest that the failed run pushed.

Find the version to verify first. A tag releases a component only when
the component's outputs changed, so the newest tag can have no image
of the component. Use one of these:

* `gh release view <tag> -R liken-sh/liken` lists each component and
  its version at that tag. A component that the tag did not release
  shows the version of its earlier release.
* `cd ci && go run . scans -root ..` prints each published image at
  the version that a cluster pulls. For a tracked component, that is its newest
  published version, which can be a development build. For a pinned
  component, it is the pinned tag.

To verify an image on a workstation, run
`liken verify image <image>:<version>`, which the paragraph after the
commands describes. The commands below show what that check does. A
workstation has no `cosign` on its `PATH`, so run the pinned cosign
through docker, from the top of the repository:

```sh
cosign=$(awk -F' = ' '$1 == "cosign" { gsub(/"/, "", $2); print $2 }' toolchains/images.toml)
docker run --rm "$cosign" verify ...
```

Resolve the image's tag to a digest once, and read everything by that
digest:

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
`liken/verify/`. It resolves the tag to the index's digest, verifies
the signature against the same identity, and reports the signer and
the package count of each platform's SBOM. `liken sbom <reference>`
runs the same check and then prints the SBOM's packages. With
`-json`, it prints the SPDX document. The commands above remain the
reference for what the check does.

To list the packages of the SBOM that came from a record, with the
purl and the CPE of each:

```sh
docker buildx imagetools inspect "$image@$digest" --format '{{ json .SBOM.SPDX }}' |
  jq -r '.packages[] | select((.sourceInfo // "") | test("/usr/share/doc/liken/.*[.]cdx[.]json"))
    | [.name, .versionInfo, (.externalRefs[]?.referenceLocator)] | join(" ")'
```

`cosign verify` checks that Fulcio issued the certificate to a
workflow of this repository, from GitHub's OIDC issuer. It checks that
the workflow ran at `main` or at a release tag, that the signature
covers the digest, and that Rekor has the signature. A branch or a
pull request never publishes, and the identity rejects a signature
from any other ref. To require one component, put its workflow in the
identity, as in `component-git-csi-driver\.yaml`.

The SBOM and the provenance have no signature of their own. The index
lists each of them by its digest. So when you read them through the
digest that `cosign verify` checked, they are the ones that the
signature covers. The same is true of the labels and the annotations.
The annotations of the index are part of the bytes that the digest
identifies. The index lists each image manifest by its digest, and
each manifest lists its config by its digest.

## The OS release's SBOM and signatures

Each OS release includes `sbom.cdx.json`, a CycloneDX SBOM, at
`https://releases.liken.sh/<version>/sbom.cdx.json`. `release.yaml`
lists it by its digest like every other artifact, so each machine's
boot slot has a copy too. `make release` writes the SBOM before the
bundle, with `liken/releases/sbom/scan.sh`, from these sources:

* Each `pin.toml` of a pin that the release ships, such as the kernel,
  k3s, and the firmware. Each entry has its purl, version, sha256,
  license, and download URL. An entry with `scope = "excluded"`, which
  only the build reads, keeps that scope.
* buildroot's SBOM, `liken/userland/dist/sbom.cdx.json`, for the
  static programs and every package they link. Host packages have the
  scope `excluded`.
* syft, pinned in `toolchains/images.toml`. It scans the root
  filesystem that the image build staged, each image archive in it,
  the toolkit, and the programs that k3s extracts from its own binary.
  `liken`'s own Go programs and their modules come from this scan, and
  so do the containerd, runc, and CNI plugins of k3s.

`liken/releases/sbom` merges them. `scan.sh` assigns some paths
to a manifest or a buildroot package, such as `/bin/k3s`. When syft
finds a program at one of those paths, the SBOM keeps the component
from the manifest or from buildroot, and the modules that the program
links depend on that component. The subject, `metadata.component`, is
the release at its CalVer version. The `compositions` say `complete`
for buildroot's packages and for each of `liken`'s own Go programs.
They say `incomplete` for the release as a whole and for k3s, because
syft identifies only programs that record what they contain. Before
it writes the file, the merge checks the document against the
CycloneDX schema in `liken/releases/sbom/schema/`. The SBOM of the
lab's working-tree channel lists only the manifests and buildroot,
with no syft scan.

Count the components by source, and read one:

```sh
sbom=releases/dist/<version>/sbom.cdx.json
jq -r '.components[] | .properties[] | select(.name == "liken:source") | .value' "$sbom" | sort | uniq -c
jq '.components[] | select(.name == "k3s")' "$sbom"
jq '.compositions' "$sbom"
```

The publish job signs `release.yaml` and every file that it lists,
keyless, with the cosign that `toolchains/images.toml` pins. It signs
them through `liken/releases/sign.sh`, before `publish.sh` uploads the
directory. Fulcio certifies `component-liken.yaml` at the release tag,
and Rekor records each signature. The Sigstore bundle of each file is
next to it, at `<file>.sigstore.json`. To verify a downloaded file:

```sh
v=2026.11.01-001
curl -fsSLO "https://releases.liken.sh/$v/release.yaml"
curl -fsSLO "https://releases.liken.sh/$v/release.yaml.sigstore.json"
cosign verify-blob release.yaml --bundle release.yaml.sigstore.json \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate-identity-regexp '^https://github\.com/liken-sh/liken/\.github/workflows/component-liken\.yaml@refs/tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}$'
```

The OS publishes only from release tags, so the identity has no
branch. `liken verify release -digest sha256:<hex> <version>` runs
this check on `release.yaml` and on every artifact that it lists. It
also checks each artifact's digest and the SBOM's schema.
`liken sbom <version>` checks `release.yaml` and the SBOM the same
way, and prints the SBOM's packages. Both checks are in
`liken/verify/`. `liken/verify/latest.sh` reports and bumps the
Sigstore trusted root that they use. A verified `release.yaml` also
vouches for each artifact by its digest, the same way it does on a
machine.

To rehearse the signing on a lab release, sign with a throwaway key.
The script then writes nothing to the public Rekor log, so the
verification skips the log. Run the commands from `liken/`:

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

Every finding goes to the repository's Security tab, under code
scanning. No finding fails a run. To list the open alerts with the
category of each, and to see which ref and category each upload of a
tool went to:

```sh
gh api --paginate 'repos/liken-sh/liken/code-scanning/alerts?state=open&per_page=100' \
  --jq '.[] | [.most_recent_instance.category, .rule.id, .rule.security_severity_level] | @tsv'
gh api 'repos/liken-sh/liken/code-scanning/analyses?tool_name=Grype' \
  --jq '.[] | [.created_at, .ref, .category] | @tsv'
```

To list the open alerts of one image, select its category,
`trivy/<image>`. A component with a second image, such as
`audio-operator-cli`, has a category for each image, so query each
one. The upload action can add a `/` to the end of a category, so the
filter removes it before it compares:

```sh
gh api --paginate 'repos/liken-sh/liken/code-scanning/alerts?state=open&per_page=100' \
  --jq '.[] | select((.most_recent_instance.category | rtrimstr("/")) == "trivy/audio-operator")
    | [.rule.id, .rule.security_severity_level, .most_recent_instance.location.path] | @tsv'
```

The open alerts on the default branch are what the Security tab shows,
and the commands above read those alerts. Code scanning files each
upload under the ref that the upload gives, and the tab's view of the
default branch shows only the uploads on `main`. So when a run is on a
release tag, every upload of its scan gives `main` as the ref, with
the run's commit. `ci/templates/sarif.tmpl` sets these two inputs.

* **Each image.** The publish job of a component runs Trivy, pinned in
  `ci/trivy.go`, on each image that it pushes. It uploads the findings
  that have a fixed version under the category `trivy/<image>`.
* **The OS.** The OS publishes no image, and no package database
  records some of the files that it ships, such as the kernel and k3s.
  So each `pin.toml` entry of such a file sets the CPE that NVD lists
  the upstream under. `liken/releases/sbom/vulnerabilities.sh` runs grype, pinned
  in `toolchains/images.toml`, over an SBOM of the OS. Trivy matches
  no component by its CPE, so this is the only scan that uses grype.
  The script leaves out each component with the scope `excluded`, and
  keeps each finding that has a fixed version. It reads `grype.yaml`,
  next to it, which lists each finding that is false for every
  release, with the reason.
* **Each build of the OS.** The OS's `build` job runs
  `make vulnerabilities` after the smoke drills. The target writes the
  SBOM of what the commit built, with the same syft scan that a
  release runs, and scans it. The job's entry in `liken/package.toml`
  sets `grype = true`. That makes the generated job upload
  `grype/liken.sarif`, the file that the job's command writes. `ci`
  rejects a job that sets the field and does not write that file. The
  upload goes under the category `grype/liken`. So that category
  contains the findings for the tree at `main`, and a finding that a
  bump can clear shows up before a release ships it.
* **Each published release.** The step "scan the release's SBOM" of
  the OS's publish job uploads under the category
  `grype/liken-release`. The findings for the tree and for the newest
  release each have their own category, because code scanning keeps
  one set of results per category, and each upload replaces the set
  before it.
* **Every third day.** `ci/` generates `.github/workflows/scan.yaml`
  from `ci/templates/scan.yaml.tmpl`. The workflow runs on every third
  day of the month, on the schedule that the template's `cron` line
  sets and its comment explains, and on a manual dispatch.
  `go run . scans -root ..` in `ci/` lists every published image at the version that a cluster pulls, and the
  newest OS release on the channel. The `trivy` job scans each image.
  The `liken` job fetches the release's SBOM with `liken sbom -json`,
  which checks its signature. Then the job scans the SBOM with grype
  and uploads under `grype/liken-release`. The schedule is a backstop.
  A vulnerability that is recorded after a publish produces no alert
  until a later scan, and the vulnerability databases publish no event
  that a workflow can subscribe to.
* **A change to the Trivy configuration.** A push to `main` that
  changes `ci/trivy.go` or `ci/templates/trivy.tmpl` scans every
  published image again. `Planner.PublishedImages` in `ci/rescan.go`
  lists the images and the versions for this scan and for the
  scheduled one.

To scan on a workstation, you need docker and `jq`. Run the commands
from `liken/`. Each command reproduces the alerts of one category:

- `make vulnerabilities` scans what your tree builds. It reproduces
  the alerts of `grype/liken`. The target depends on the `image` and
  `cli` targets in `liken/Makefile`, so it builds the image and the
  CLI first when they are out of date.
- The scan of a release's SBOM reproduces the alerts of
  `grype/liken-release`. Use the version of the newest release on the
  channel, which `cd ci && go run . scans -root ..` prints:

  ```sh
  go run ./cli sbom -json 2026.11.10-001 >sbom.cdx.json
  releases/sbom/vulnerabilities.sh sbom.cdx.json
  ```

A scan of the tree does not reproduce an alert of the release
category, because the tree can have pins that are newer than the
release.

To scan one image the way a publish job does, run Trivy at the
`trivyVersion` in `ci/trivy.go`. `toolchains/images.toml` does not pin
Trivy, because the workflows install it from a release archive:

```sh
version=$(sed -n 's/^const trivyVersion = "\(.*\)"/\1/p' ci/trivy.go)
docker run --rm "aquasec/trivy:$version" image --scanners vuln --ignore-unfixed <image>
```

The scan does not match `linux-firmware`, which NVD does not list. It
also does not match the iptables programs of `k3s-root-xtables`,
whose entry records the version of k3s-root. Trivy also reports no
finding for a Debian package of an image built from Debian sid, and
`plans/open-problems/trivy-matches-nothing-on-debian-sid.md` has the
evidence.

## Answer a finding

No finding fails a build, so a person decides what to do with each
alert. Ask these questions in order. Stop at the first answer that
tells you what clears the alert.

1. **What does the alert say?** Read it in full by its number, which
   is the last part of its URL in the Security tab:
   `gh api repos/liken-sh/liken/code-scanning/alerts/<number>`. The
   output shows the rule, its severity, the category, the path, and
   the message, which names the package and its version. The path
   tells you how the scanner found the package. A path to a program,
   such as `bin/k3s`, means that grype matched a Go module that the
   program links. The path `sbom.cdx.json` means that grype matched a
   component that a `pin.toml` or buildroot names, such as a kernel
   that it matched by the `cpe` of its `pin.toml` entry. The comment at the end of
   `liken/releases/sbom/vulnerabilities.sh` gives this rule.
2. **Which category is it in?** An alert in `grype/liken-release`
   with no match in `grype/liken` is a finding that `main` already
   fixed. The next release that includes the fix clears it.
3. **Did the scanner match the right product and version?** Compare
   the alert's package and version with the entry's `cpe` in the
   pin's `pin.toml`. When the CPE names the wrong product or version,
   correct it in the `pin.toml` and in the `set_pin` call of the pin's
   `latest.sh`, because each bump writes the CPE again.
4. **Is the finding false for every release?** An example is an NVD
   record whose version range has no upper limit, or a flaw on a
   platform that `liken` does not build for. Add an entry to
   `liken/releases/sbom/grype.yaml`, with a comment and a `reason`.
   The comments in that file show the form. Code scanning does not
   show a finding that the file drops, so the reason must be one that
   a reviewer can check.
5. **Does a newer release of the upstream include the fix?** Run the
   pin's `latest.sh` with no argument. When the pin is behind, bump it
   with the `bump-components` skill. For a Debian package of a base
   image, the pin is the Debian snapshot, which `vulkan/latest.sh`
   moves for every base.
6. **Is the pin current, with no fix upstream?** Do nothing. The
   alert stays open until upstream releases a fix, and no one
   dismisses it. A module that k3s links is an example: `liken`
   ships the k3s release as k3s built it.

What else the project could do with a finding that no bump clears,
such as a check that fails a build, is not decided.
`plans/open-problems/what-a-vulnerability-finding-does-to-a-build.md`
records the decisions so far and the ideas that are not built.
