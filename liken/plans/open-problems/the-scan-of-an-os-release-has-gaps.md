# The scan of an OS release has gaps

Open problem.
[Milestone 96](../completed/96-code-scanning-reads-the-sbom-of-each-os-release.md)
scans the SBOM of each OS release with grype and uploads the findings
to code scanning, the tree's under the category `grype/liken` and a
release's under `grype/liken-release`. The items below are
what the scan does not match, what it reports that no bump of today
clears, and what is not proven until the workflows run on GitHub. Each
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

Seen on 2026-11-11 against a build of the tree with util-linux
2.42.4, which reported 21 findings:

- **libevent 2.1.13.** CVE-2026-63380 is fixed only in 2.2.2-alpha,
  and libevent has no stable release with the fix.
- **20 findings in the programs that k3s ships.** `liken` ships k3s's
  release as k3s built it, so each clears when k3s releases a build
  with the newer module.

## What is not proven until a run on GitHub

- **The ref of a release's upload.** The OS publishes on a release
  tag, so the publish job's upload names `refs/tags/<version>`.
  Whether code scanning shows that upload in the view of the default
  branch was not checked. The Trivy upload of a release publish has
  the same property. The scheduled scan uploads on `main`.
- **The scheduled workflow.** The cron trigger, the cache restores,
  and whether `upload-sarif` accepts grype's SARIF have not run.

## A change to the scan's settings

A push to `main` that changes `ci/trivy.go` or
`ci/templates/trivy.tmpl` scans every published image again. A change
to `vulnerabilities.sh`, to `grype.yaml`, or to the `grype` entry of
`toolchains/images.toml` scans no release, so `grype/liken-release`
holds the findings of the old settings until the next scheduled scan,
at most three days later. `grype/liken` takes the new settings at the
next build of the OS.
