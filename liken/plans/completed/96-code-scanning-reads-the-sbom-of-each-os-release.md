# 96. Code scanning reads the SBOM of each OS release

Milestone 96. Proposed and built on 2026-11-11. The scan ran by hand
on a workstation against two SBOMs ([As built](#as-built)). No run
of `.github/workflows/scan.yaml` and no upload to code scanning has
happened yet: the first push to `main` and the first release tag after
this milestone are their first runs.
[The scan of an OS release has gaps](../open-problems/the-scan-of-an-os-release-has-gaps.md)
holds what this milestone did not build.

A known vulnerability in the kernel that `liken` ships raised no alert
in the repository's Security tab on GitHub, and neither did one in
k3s, the bootloaders, the firmware, or a program that buildroot
builds. A person learned of one only by reading the upstream's
announcements, as with the kernel bump of
[milestone 90](90-the-kernel-moves-to-7-3-1-for-a-tcp-use-after-free.md).
This milestone scans the SBOM of each OS release against the
vulnerability databases and uploads the findings to code scanning, so
the Security tab shows what the OS ships beside what each image ships.

The text from "Why no alert reaches GitHub today" to the end of "What
the scanners reported on 2026-11-11" is the problem and the evidence
as they were written before the build.

## Why no alert reaches GitHub today

[Root plan 86](../../../plans/completed/86-known-vulnerabilities.md) gives the Security tab three
sources, and none of them reads the OS release:

- **Trivy** scans each container image that CI publishes
  (`scanImage` in `ci/trivy.go`). The OS publishes files to the
  release channel and no image, and `ScansImages` in `ci/rescan.go`
  leaves it out. On 2026-11-11 the repository's code scanning held 48
  `trivy/<image>` categories and no category for the OS.
- **CodeQL** reads `liken`'s own Go and Rust code.
- **Dependabot** reads each `go.mod` and `Cargo.lock`. GitHub's
  dependency graph of the repository on 2026-11-11 named no kernel:
  the only packages with `linux` in the name were Rust crates.
  Nothing in the repository calls the dependency submission API.
  A submission would not help, because Dependabot raises an alert
  only for a package of an ecosystem that GitHub's advisory database
  covers, such as Go or cargo, and the kernel is in none of them.

The release SBOM, `sbom.cdx.json`, does name the kernel.
`kernel/pin.toml` writes it as `linux-image-unsigned` and
`linux-modules`, each with a `pkg:generic` purl, the CycloneDX type
`file`, and no CPE. No job scans that file. A scanner that did would
find no advisory for the kernel, because a `pkg:generic` purl names
no ecosystem and the entry has no CPE to match against NVD.

## What the scanners reported on 2026-11-11

The scans read the SBOM of the lab release `2026.11.04-901`, which
holds 809 components: 721 from syft, 64 from buildroot, and 24 from
`pin.toml` manifests. The kernel in it is 7.3.1 and k3s is
`v1.37.1+k3s1`. The scanners were grype 0.121.3 with its database of
2026-11-11, and Trivy 0.76.1, the version that `ci/trivy.go` pins.

| SBOM | grype findings | grype kernel findings | Trivy findings |
| --- | --- | --- | --- |
| As released | 178 | 0 | 70 |
| Kernel entry with a CPE, type `file` | 178 | 0 | not run |
| Kernel entry with a CPE, type `operating-system` | 178 | 0 | not run |
| Kernel entry with a CPE, type `application` | 246 | 68 | 70 |
| The same, with the version set to 7.2.6 | not counted | 143 | not run |

The CPE was `cpe:2.3:o:linux:linux_kernel:<version>:*:*:*:*:*:*:*`,
the name that NVD lists the kernel under.

- **grype matches the kernel by CPE, and only for a type it keeps.**
  grype reads an SBOM through syft's CycloneDX decoder, which drops a
  component of type `file` or `operating-system`. The open problem
  [some SBOM components name the wrong thing](../../../plans/open-problems/some-sbom-components-name-the-wrong-thing.md)
  recorded this for syft on 2026-11-01. The same rule drops k3s and
  each of the other 21 `file` components.
- **The match finds the vulnerability of milestone 90.** With the
  version set to 7.2.6, the kernel that milestone 90 replaced, grype
  reported CVE-2026-98432 among 143 kernel findings, 76 of them with a
  fixed version.
- **Most findings against a current kernel are old NVD records.** Of
  the 68 findings against 7.3.1, 67 have no fixed version, and all 68
  name a CVE of 2016 through 2023. NVD's record of each gives a
  version range with no upper limit. The one with a fixed version,
  CVE-2020-27815, lists fixes only in the 4.9 through 5.10 series. A
  scan that keeps only a finding with a fixed version drops the 67,
  as `scanImage` does with `--ignore-unfixed`.
- **Trivy does not match by CPE.** It reported the same 70 findings
  with and without the kernel's CPE, all of the class `gobinary`. It
  reads operating system package databases and language ecosystems,
  and the kernel that `liken` ships is in neither.
- **The scan finds more than the kernel.** Of grype's 178 findings on
  the SBOM as released, 146 have a fixed version, and 130 of those
  are in the four programs that k3s ships: `/bin/k3s`, `/bin/runc`,
  `/bin/containerd-shim-runc-v2`, and `/bin/cni`. They are built with
  Go 1.26.8. 13 findings are critical: GO-2026-6604 and GO-2026-6612,
  which Go 1.26.9 and `golang.org/x/net` 0.60.0 fix. No alert in the
  Security tab names any of them today.
- **grype ignores the CycloneDX scope.** 40 findings came from a CPE
  match, and they include buildroot's host tools, such as `binutils`,
  `gcc-final`, `bison`, and `python3`. The SBOM gives each of those
  the scope `excluded`, because it ran during the build and the
  release does not ship it.

## The decisions

- **grype scans the OS, and Trivy scans every image.** The maintainer
  runs trivy-operator in clusters, so the images stay on Trivy and
  one tool reports on an image in CI and in a cluster. Trivy matches
  no component by its CPE, and a CPE is the only name that NVD has
  for the kernel, the bootloaders, the microcode, and k3s, so the scan
  of a release's SBOM is the one place that runs grype.
- **The kernel takes the type `application`.** CycloneDX's
  `operating-system` describes a kernel better, and syft's decoder,
  which grype reads an SBOM through, drops it. The microcode takes
  `firmware`, which the decoder keeps.
- **A scan runs every third day.** The vulnerability databases publish
  no event that a workflow can subscribe to, so a schedule is the
  backstop for a vulnerability that is recorded after a publish. The
  schedule scans every published image with Trivy too, because the
  images had the same gap.
- **The scan keeps only a finding with a fixed version**, as the image
  scans do.

## As built

1. **The pins name what NVD names.** The entries
   `linux-image-unsigned`, `k3s`, `grub-pc-bin`, `systemd-boot-efi`,
   and `intel-microcode` state a `cpe` and a `type` that a scanner
   keeps. NVD lists an upstream at its own version, so `cpe_version`
   in `toolchains/pin.sh` gives the part of a tag or a package version
   that is the upstream's, each `latest.sh --bump` writes the CPE with
   it, and `cpeFindings` in `ci/manifests.go` accepts a CPE at the
   entry's version or at that part of it. `linux-firmware`,
   `wireless-regdb`, `hwdata`, and `k3s-root-xtables` state a type and
   no CPE.
2. **The scan.** `liken/releases/sbom/vulnerabilities.sh` runs grype,
   the `grype` entry of `toolchains/images.toml`, over an SBOM. It
   leaves out each component with the scope `excluded`, passes
   `--only-fixed`, and reads `grype.yaml` beside it. `grype.yaml` sets
   `match.dpkg.using-cpes`, because grype otherwise matches a package
   of Ubuntu's archive only against a package database, and it names
   three findings that are false for every release, each with its
   reason: CVE-2020-27815, CVE-2023-4039, and GO-2026-5932 in `liken`'s
   own programs. The script writes SARIF with a path on each finding.
3. **The uploads.** The OS's publish job runs the script on the
   release that it publishes, and the `liken` job of
   `.github/workflows/scan.yaml` runs it on the newest release of the
   channel, which it fetches with `liken sbom -json`. Both upload under
   the category `grype/liken`. `scan.yaml` runs at 04:43 UTC on every
   third day of a month and on a dispatch, and its `trivy` job scans
   every published image. `go run . scans` in `ci/` plans it, and
   `Planner.PublishedImages` in `ci/rescan.go` gives the images for it
   and for the rescan after a change to the Trivy configuration.
4. **The manual.** The verify guide and its skill give the grype
   command for a release and say what Trivy does not match. The
   release channel reference, the root `AGENTS.md`, and the `releases`
   and `bump-components` skills state the scans.

## What the scan reported after the build

grype 0.121.3 with its database of 2026-11-11, through
`vulnerabilities.sh`:

| SBOM | Components scanned | Findings |
| --- | --- | --- |
| Release `2026.11.10-001`, from the channel, k3s `v1.37.1+k3s1` | 755 of 809 | 135 |
| Lab release `2026.11.11-901`, from this tree, k3s `v1.37.2+k3s1` | 769 of 823 | 23 |

The tree of the lab release holds the bump of k3s to `v1.37.2+k3s1`
and of `k3s-root` to `v0.15.3`, which the scan's findings led to. k3s
builds its programs with Go 1.26.10 in that release and with Go 1.26.8
in the one before, and most of the 135 findings were in Go's standard
library. `make smoke-uefi` booted the lab release to `Ready`.

Of the 23 findings that remain, 20 are in the programs that k3s ships
(`/bin/k3s`, `/bin/runc`, and `/bin/containerd-shim-runc-v2`), in the
modules `golang.org/x/crypto` v0.54.0, `golang.org/x/net` v0.60.0,
`github.com/cilium/ebpf`, `runc` v1.4.2, and five others. A k3s
release with newer modules clears them. Two are in util-linux 2.41.5
and one is in libevent 2.1.13, and the open problem named above holds
both.

With the kernel's version set to 7.2.6 in a copy of the SBOM, the scan
reported CVE-2026-98432, the vulnerability of milestone 90. Against
7.3.1 it reports no kernel finding with a fixed version.
