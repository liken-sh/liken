# Trivy matches no advisory on a Debian sid image

Open problem. Trivy lists the Debian packages of each image that
installs from Debian sid, and it matches none of them against an
advisory. So the scan that CI runs on each published image reports no
Debian finding for any image on the bases, whatever those packages
hold.

## What the scan reports

`scanImage` in `ci/trivy.go` runs Trivy at `trivyVersion` on each
image that a publish job pushes, with `--scanners vuln
--ignore-unfixed --format sarif`, and the job uploads the SARIF file to
code scanning under the category `trivy/<image>`.

The bases `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston`, and
`astrometry`, install from one snapshot of Debian sid
(`vulkan/latest.sh`), and every image built on a base carries that
base's packages. Each base is a closure that records its packages in
`/var/lib/dpkg/status.d/` and copies `/etc/os-release` and
`/etc/debian_version` (the Closures section of the root `AGENTS.md`).
Trivy reads that record and detects the OS as `debian forky/sid`.

On 2026-11-01, Trivy 0.76.1, the version that `ci/trivy.go` pinned
that day, with the vulnerability database of that day:

| Image | OS that Trivy detected | Packages | Findings |
| --- | --- | --- | --- |
| `ghcr.io/liken-sh/mpv:20261101-1` | `debian forky/sid` | 220 | 0 |
| `debian:sid-slim` | `debian forky/sid` | 76 | 0 |
| `debian:trixie-slim` | `debian 13.7` | 78 | 175 |

The counts of findings are without `--ignore-unfixed`. With it, as CI
runs the scan, the `mpv` image has 0 findings too. On each sid image
Trivy prints the warning `This OS version is not on the EOL list
family="debian" version="forky/sid"`, and then
`Detecting vulnerabilities... os_version="forky/sid" pkg_num=220`.
The scan does not fail. It lists the packages and matches no
advisory to any of them.

To repeat the check on a workstation, with the version in
`trivyVersion`:

```sh
docker run --rm -v /var/run/docker.sock:/var/run/docker.sock \
  aquasec/trivy:<trivyVersion> image --scanners vuln --ignore-unfixed \
  ghcr.io/liken-sh/mpv:<tag>
```

## What a reader of the Security tab sees

A scan with no findings uploads a SARIF file with no results, so code
scanning shows no open Trivy alert under `trivy/<image>` for any image
on the bases. Plan 86 in `completed/` describes the Security tab as
the place where every known vulnerability is listed, and plan 87
builds on that. A reader who opens the tab sees the same state for an
image whose Debian packages hold no known vulnerability and for an
image that Trivy did not check against Debian's advisories, and has
no indication of which state applies.

The Go modules and the Rust crates in the same images are a separate
part of the scan. Trivy reads them from each binary's build info and
from the `.dep-v0` section that cargo-auditable writes, so this
problem does not apply to them.

## Scope

Every image that ships the packages of a sid base is affected. The
`indi` images install from Ubuntu's snapshots, and this check did not
scan them.
