# 86, Known vulnerabilities

Proposed on 2026-10-31. Not built.

CI runs no vulnerability scanner. A pin with a published advisory stays
in the tree until `make versions` reports it as `behind` and someone
bumps it. `make versions` compares versions, not advisories, so it
cannot say which bump fixes a vulnerability, and it says nothing about
a vulnerability in a version that is still the newest release. A
release that is already published gets no check at all after its tag.

This plan gives `liken` three parts:

- **Find.** CI scans every Go module, every Rust lock file, and every
  image, and fails the build on a finding that a bump can fix.
- **Fix.** Renovate opens one pull request that moves every copy of a
  dependency at once, as soon as an advisory names it.
- **Show.** Each release publishes a software bill of materials (SBOM)
  for each image and for the OS, so anyone can match a new advisory
  against a release that is already out.

Each part uses the tools that open source projects use for this work.
`liken` adds the rule that sorts a finding into fixable or not, and
the SBOM of the OS, which no tool writes for a system built from
source pins.

## Evidence

On 2026-10-30, `govulncheck ./...` and `osv-scanner scan source` ran by
hand against every shipped Go module and every `Cargo.lock`:

- `golang.org/x/net` v0.58.0 carried five HTTP/2 advisories,
  GO-2026-6603, GO-2026-6610, GO-2026-6611, GO-2026-6612, and
  GO-2026-6617, fixed in v0.60.0. `govulncheck` found a call path to
  them in `kubernetes/`, `liken/`, and every operator, through
  `apiclient.Client.send` and client-go's HTTP/2 transport.
- `golang.org/x/image` v0.41.0 carried three WebP decoder advisories,
  GO-2026-4961, GO-2026-5061, and GO-2026-6222, fixed by v0.45.0. Of the
  15 modules that require it, `govulncheck` found a call path only in
  `people-operator`.
- `rustls` 0.23.45 in `media-operator` carried RUSTSEC-2026-0346 to
  RUSTSEC-2026-0349, fixed in 0.23.46.
- `tokio` and `lru` carried one advisory each, and `ttf-parser`,
  `paste`, and `unsafe-libyaml` are marked unmaintained.

Commits 4d3b7a71 and 1e1105af cleared every finding that a bump could
reach, and release `2026.10.31-001` shipped them. Two facts from that
work shape this plan:

- `toolchains/latest.sh --bump go:<module>` moves only the modules
  that a `go.mod` requires directly. `golang.org/x/net` was indirect
  in most modules, so the fix needed `go get -u ./...` in each module.
  The k8s.io modules then had to go back to the versions that k3s
  names.
- `lru` 0.16.4 still carries RUSTSEC-2026-0253, because `cryoglyph`
  requires `lru = "^0.16"` and the fix is in 0.18.2. No change in this
  repository reaches it until `iced` releases.

On 2026-10-31, the repository had Dependabot alerts, secret scanning,
and push protection turned on, with no open alert. Dependabot security
updates and CodeQL were off.

## The rule

A finding that a bump can fix fails the build. A finding that no bump
can fix only warns, and the warning names what blocks the fix.

The rule matches [plan 83](completed/83-every-pin-stays-current.md):
a fix that is published is a pin that is behind, and a blocked fix is
a hold. A hold states its reason in its row, so a blocked finding
states its blocker in its warning.

## Find

A new `ci vulns` command runs the scanners and applies the rule. None
of the scanners can apply the rule on its own: `govulncheck`,
`cargo deny`, `osv-scanner`, and `trivy` each exit non-zero for every
finding. So `ci vulns` reads each tool's JSON and sorts each finding.

- **Go.** `govulncheck -json` runs in each module. It reports a
  vulnerability only when the module's code can reach it, so a finding
  is worth acting on. A finding is fixable when it has a
  `fixed_version`. Go's minimum version selection takes the highest
  version that any requirement names, so a `require` line reaches a fix
  even for an indirect module. A finding in the standard library is
  fixable by a Go toolchain bump.
- **Rust.** `cargo deny check advisories` runs in each workspace. A
  patched version is not enough for a Rust finding, because a semver
  range elsewhere in the graph can block it. A finding is fixable when
  `cargo update --precise <fixed> --dry-run` resolves, and the warning
  for a blocked one names the crate whose range blocks it.
  `cargo deny` also reports a crate that is unmaintained, which only
  warns.
- **Images.** `trivy image` runs on each image after its bake target
  builds, before its smoke check. A finding in a package of a base
  image is fixable when the snapshot of sid that `vulkan/latest.sh`
  reports holds the fixed version, because a new snapshot is the bump.
  A finding in a Go binary or a crate inside an image is the same
  finding as the source scan's, so `ci vulns` reports it once.

`ci vulns` runs as a job of each component that ships Go, Rust, or an
image, from its `package.toml`, so `ci/` runs it when the component
changes. A component that does not change gets no scan from CI, which
is the gap that [Fix](#fix) covers.

## Fix

An advisory arrives with no commit, so a check that runs only on a
push misses it. The advisory databases offer no subscription, and a
daily scan is the timer that `AGENTS.md` calls a defect. GitHub's
dependency graph is the one source that sends an event: it matches each
new advisory against the `go.mod` and `Cargo.lock` files of the
repository and raises a Dependabot alert.

Renovate acts on those alerts. Its `vulnerabilityAlerts` setting reads
the repository's Dependabot alerts and opens a pull request at once,
outside its schedule. Renovate replaces Dependabot's own pull requests
for one reason: Dependabot groups a security update only within one
directory, so each of the 14 Go modules gets its own pull request, and
each one fails `ci pins` because the other 13 copies still name the
old version. A Renovate `groupName` collects one dependency from every
package file into one pull request, so every copy moves together.

Renovate also opens the routine bumps that `bump-components` makes by
hand today, under the rule of plan 83:

- One group for the security fixes, opened at once.
- One group each for the Go modules, the crates, and the toolchain
  images in `toolchains/images.toml`, on a weekly schedule.
- `gomodTidy` after each Go bump, and indirect Go modules in scope,
  because the `golang.org/x/net` fix was indirect.
- No updates for `k8s.io/*` and `sigs.k8s.io/*`, which follow the k3s
  release in `liken/k3s/VERSION`.
- Later, custom regex managers for the `VERSION` files of the OS, so
  the OS pins reach the same pull requests. Until then,
  `bump-components` keeps them.

Renovate runs as Mend's hosted app, which is free for a public
repository, so `liken` runs no scheduler of its own. Dependabot alerts
stay on, because they are Renovate's event source. Dependabot security
updates stay off.

CodeQL default setup turns on in the same step. It reads the
repository's own code, not its dependencies, so it finds a different
kind of fault from the scanners above.

## Show

A finding that arrives after a release has nothing to match against
unless the release says what it holds. So each release publishes an
SBOM in CycloneDX format:

- **Each image.** `docker buildx bake` writes an SBOM attestation for
  each image with `--sbom=true`, and the publish pushes it beside the
  image on ghcr.io. BuildKit's SBOM generator, Syft, reads the Debian
  packages, the Go binaries, and the Rust binaries.
- **The OS.** No tool can read the OS's pins, because the OS builds
  from source pins: the `VERSION` files, `licensing/sources.sh`, and
  `toolchains/images.toml`. A new generator in `liken/releases/` writes
  one component for each pin, with a package URL (purl) or a CPE that
  the advisory databases match, and `release.yaml` lists the SBOM
  beside the other artifacts with its sha256. The catalog digest
  covers `release.yaml`, so it also covers the SBOM.

With the SBOMs published, `osv-scanner scan --sbom` checks any release
against today's advisories. Signing the images and their attestations
with Sigstore `cosign`, publishing VEX statements for findings that do
not apply, and OWASP Dependency-Track as a service that watches the
published SBOMs are later steps. They are worth their cost when
someone outside the project runs `liken` releases and needs the record.

## The kernel

The kernel's CVE Numbering Authority publishes hundreds of CVEs each
month, one for nearly every fix that goes into a stable series. OSV
lists them under the `Linux` ecosystem. A kernel finding therefore
only warns. The kernel stays current under plan 83 by following the
newest stable release, and a kernel bump for one CVE, as in liken plan
90, is a decision someone makes after reading that CVE.

## The decisions it needs

- **An image finding that a new snapshot fixes.** sid takes uploads
  every day, so a fix in sid that the pinned snapshot lacks is common.
  Under the rule, it fails every image build until someone bumps the
  snapshot. The other choice is to make an image finding warn and to
  make `vulkan/latest.sh` report the snapshot as `behind, with fixes`
  when it is.
- **The OS SBOM's identifiers.** Some pins, such as `linux-firmware`
  and `wireless-regdb`, have no purl or CPE that any database matches.
  The generator can list them by name only, which no scanner checks,
  or leave them out.
- **Renovate's scope at the start.** Renovate can take only the
  security group at first, while `bump-components` keeps the routine
  bumps, or it can take every Go module, crate, and toolchain image
  from the start.

## Order

1. Turn on CodeQL default setup, and add Renovate with the security
   group only.
2. Write `ci vulns` for Go and Rust, with the fixable rule, and add its
   job to each component's `package.toml`.
3. Add the image scan to `ci vulns`, after the decision on snapshot
   findings.
4. Write the image SBOMs, then the OS SBOM generator and its entry in
   `release.yaml`.
5. Move the routine Go, crate, and toolchain bumps to Renovate, and
   update `bump-components` to say which pins Renovate owns.
