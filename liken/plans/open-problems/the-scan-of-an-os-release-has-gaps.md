# The scan of an OS release has gaps

Open problem.
[Milestone 96](../completed/96-code-scanning-reads-the-sbom-of-each-os-release.md)
scans the SBOM of each OS release with grype and uploads the findings
to code scanning, the tree's under the category `grype/liken` and a
release's under `grype/liken-release`. The items below are
what the scan does not match, what it reports that no bump of today
clears, and what is not proven until a release runs. Each
item is independent.

## What the scan does not match

- **`linux-firmware`.** NVD lists no product for the firmware
  repository, so the entry has no CPE. Ubuntu's advisories name its
  own `linux-firmware` package at Ubuntu's versions, which do not
  compare with the upstream's date tag.
- **The iptables programs of `k3s-root-xtables`.** The entry's version
  is k3s-root's, `v0.15.3`, and the programs are iptables 1.8.11. NVD
  lists `netfilter:iptables`. `ci pins` accepts a CPE only at the
  entry's version, and the `iptables-source` entry, which holds
  1.8.11, has the scope `excluded`, which the scan leaves out.
- **A kernel record with no fixed version.** The scan keeps only a
  finding with a fixed version, because NVD records 67 old kernel
  vulnerabilities with a version range that has no upper limit, and
  each of those matches every kernel. Whether that rule also drops a
  true kernel finding was not checked. The kernel CVE team publishes
  its own records with the affected commit ranges, and a scanner that
  reads them would be more exact than NVD.

## What the scan reports that no bump clears today

The root open question
[what a vulnerability finding does to a build](../../../plans/open-problems/what-a-vulnerability-finding-does-to-a-build.md)
lists these findings, as seen on 2026-11-11: 20 in the programs that
k3s ships. It also listed one in libevent 2.1.13, which was false,
because no program of a release links libevent.
[Milestone 104](../completed/104-the-sbom-excludes-each-package-that-no-shipped-file-contains.md) gives
each buildroot package that no shipped file contains the scope
`excluded` in the release's SBOM, and the scan leaves it out.

## What is not proven until a release runs

Every upload of a scan names `main` when its run is on a tag, with the
run's commit, because code scanning's view of the default branch reads
only the uploads on `main`. Before that, on 2026-11-15, the publish of
`2026.11.15-001` uploaded 28 findings under its tag, and the alerts of
`main` held the 26 findings of the scheduled scan of 2026-11-13 until
the scheduled scan of 2026-11-16. No release has published since the
change, so whether code scanning accepts a tag run's upload for `main`
and shows its findings at the publish was not checked. The Trivy
upload of a release publish names `main` the same way.

## A change to the scan's settings

A push to `main` that changes `ci/trivy.go` or
`ci/templates/trivy.tmpl` scans every published image again. A change
to `vulnerabilities.sh`, to `grype.yaml`, or to the `grype` entry of
`toolchains/images.toml` scans no release, so `grype/liken-release`
holds the findings of the old settings until the next scheduled scan,
at most three days later. `grype/liken` takes the new settings at the
next build of the OS.
