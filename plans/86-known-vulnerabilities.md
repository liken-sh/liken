# 86, Known vulnerabilities

Proposed on 2026-10-31. Not built.

CI runs no vulnerability scanner, and nothing shows a known
vulnerability in `liken` in one place. A pin with a published advisory
stays in the tree until `make versions` reports it as `behind` and
someone bumps it. `make versions` compares versions, not advisories,
so it cannot say which bump fixes a vulnerability. The Debian packages
inside the images get no check at all.

This plan puts every known vulnerability in the repository's GitHub
Security tab, from tools that GitHub runs or reads:

- **Dependabot** matches each `go.mod` and `Cargo.lock` against
  GitHub's advisory database, raises an alert, and opens a pull
  request with the fix.
- **CodeQL** reads `liken`'s own code for faults, such as an injection
  or an unchecked path.
- **Trivy** reads each image that CI publishes, and its findings
  become code scanning alerts beside CodeQL's.

Secret scanning and push protection are already on and stay on. The
alerts fail no build. The Security tab is where a person reads them,
and each fix lands through a pull request that runs the usual checks.

Each scan runs only for the components that a push or a publish
changes, the same way `ci/` runs a component's builds. A scan that
reads every component on every push would add tens of minutes to each
run for nothing new. A change to Trivy's own configuration is the one
exception: Trivy then scans every published image again, as the Trivy
section says.

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

## Dependabot

GitHub's dependency graph reads every `go.mod` and `Cargo.lock` in the
repository, and Dependabot alerts match them against each new
advisory as GitHub publishes it. This is the one part that needs no
scan of ours and no timer: GitHub matches each advisory when it
arrives, which a check on a push cannot do, because an advisory
arrives with no commit. Alerts are on already.

Dependabot security updates turn on with a `.github/dependabot.yml`
that lists each Go module directory and each Cargo workspace, and
groups the security updates in each directory into one pull request.
The configuration makes no update to `k8s.io/*` or `sigs.k8s.io/*`,
which follow the k3s release in `liken/k3s/VERSION`, and it opens no
routine version updates, which `bump-components` keeps.

A pull request from Dependabot runs CI like any other, and the plan
job runs only the jobs of the components that it changes.

Dependabot groups a security update only within one directory, not
across the repository. A module that several `go.mod` files require,
such as `golang.org/x/net` in every operator, gets one pull request for
each directory, and each one fails `ci pins`, because the other copies
still name the old version. A person finishes the fix with
`toolchains/latest.sh --bump go:<module>` on one of the branches, and
closes the others. `ci/` gets a check that fails when a module or a
workspace is missing from `dependabot.yml`, so a new component cannot
go without updates.

Each alert names the version that fixes it, but not whether `liken`'s
code reaches the vulnerable function. `golang.org/x/image` v0.41.0
raised an alert in 15 modules on 2026-10-30, and `govulncheck` found a
call path in one of them. An alert that does not apply is dismissed in
the Security tab, with the reason.

## CodeQL

CodeQL reads the repository's own Go, Rust, Python, C and C++, and
GitHub Actions code. Its default setup analyzes every language of the
whole repository on every push to `main`, every pull request, and once
a week, and it accepts no path filter. So `liken` uses the advanced
setup instead: `ci/` writes a CodeQL job into each component's
workflow for the languages that the component holds, and the plan job
runs it when a file that the language's analysis reads changes, the
same rule that runs a Go job only for a Go change.

The loader in `ci/codeql.go` derives the CodeQL jobs, so no
`package.toml` declares one, and the plan and the workflow treat each
as an ordinary job:

- Each `go` job gets `codeql-<job>`, which runs `go build ./...` in the
  job's module while CodeQL traces the build.
- Each `rust` job gets `codeql-<job>`, which reads the crate in the
  job's directory with build mode `none`.
- A component that holds C or C++ sources gets `codeql-c-cpp`, and one
  that holds Python sources gets `codeql-python`, both with build mode
  `none`. No job builds those files, so the loader finds them by their
  file names, outside `testdata/`.

A CodeQL job runs when a source of its language changes in its own
directory. A Go or a Rust job also runs when a source of its language
changes in a module that the `go.mod` replaces or a crate that the
crate takes by path. A data file, a manifest, or a Makefile runs no
CodeQL job. Each job's configuration limits the analysis to its own
directory, so a module of another component is analyzed only in that
component's job.

The GitHub Actions code is the files under `.github/`, which belong to
no component. The root workflow's `codeql-actions` job analyzes them
when a file under `.github/` changes, and the plan job's `actions`
output decides that. A change to `ci/` or a `package.toml` that writes
a different workflow changes a file under `.github/` too.

Each component job uploads its results under the category
`codeql/<component>/<job>`, where `<job>` is the job's name without
`codeql-`, such as `codeql/library-operator/rust-appearances`, and the
Actions job uploads under `codeql/actions`. Code scanning closes an
alert only when a later upload in the same category no longer reports
it, so a category per job keeps the alerts of each analysis until that
analysis runs again, and an analysis of one component does not close
the alerts of another. A component has more than one job of a
language when it has two crates, so the category names the job, not
the language.

A pull request from a fork gets a token that cannot write to code
scanning, so the CodeQL jobs run only for a branch of this repository.

## Trivy

Trivy reads the operating system packages and the language packages
inside an image. It runs in each component's publish job, on each
image that the job has just pushed. `ci publish` runs the scan right
after the push, because only the publish knows which images it pushed:
an image whose version is already published is not pushed again, and
a dry run pushes nothing. The scan reads the image from the registry
by its version tag. A publish never pushes a version tag twice, so
the tag names the bytes that were just pushed, which are the bytes
that a cluster pulls. Its SARIF goes to code scanning under the
category `trivy/<image>`, from one `upload-sarif` step for each image
of the component. A component that does not publish starts no Trivy
scan, and a pinned base, which publishes only when its tag is new, is
scanned once for each tag.

The scan reports only a vulnerability that has a fixed version, with
`--ignore-unfixed`. The bases install from Debian sid, which carries
many vulnerabilities that no package fixes yet, and those would bury
the findings that a snapshot bump can act on.

A scan reports, and it does not gate the publish. A scan that fails,
for example when Trivy's database download fails, prints a warning,
and the publish pushes the next image. The install of Trivy and each
upload continue on an error too. `ci/trivy.go` pins the Trivy
version, and `ci/latest.sh` reports it.

The Trivy configuration is two files: `ci/trivy.go`, which holds the
Trivy version and the arguments of a scan, and
`ci/templates/trivy.tmpl`, which holds the steps that install Trivy,
restore its database, and upload a scan, and the root workflow's
`trivy-db` and `trivy` jobs. A publish
scans only the images that it pushes, so a change to the
configuration would reach each image only at its next publish. On
2026-10-31, the push that added Trivy, run 41210017489, changed `ci/`
and the workflows and no component's outputs, so nothing published
and Trivy scanned nothing.

So a push to `main` that changes one of the two files scans every
published image again, with no build and no push. `Planner.Rescan` in
`ci/rescan.go` decides it from the same diff that selects the jobs:
since the newest `main` run that passed, or every image when every job
runs. A change to any other file, including the rest of `ci/`, scans
nothing again, and a branch, a pull request, and a release tag never
scan again. The plan job writes the images and their versions to its
`trivy` output, and its summary says why the rescan runs or does not.
For a tracked component, the version is its newest release or
development build on ghcr.io, the one that a cluster that follows the
newest version pulls. For a pinned component, it is the published tag.
A component that publishes in the run is left out, because its
publish job scans what it pushes with the new configuration, and a
component with nothing published has nothing to scan.

The `trivy` job runs one matrix entry for each image: `ci scan` scans
the image, and the upload goes under `trivy/<image>`, the category
that the image's publish uploads under, so the new results replace the
old ones. The job continues on an error, and a scan that fails prints
a warning, so a rescan never fails the run. The alternative was to make
the configuration an input of every image, so that a change to it
publishes every component as a development build, and each publish
scans. The plan rejected it: every image would build and push again,
and every cluster that follows the development builds would restart
its pods for a change to the scanner.

### The vulnerability database

Every scan reads Trivy's vulnerability database, and a run that scans
in the `trivy` job's matrix and in each publish job would download it
once for each job: 57 images on 2026-10-31. Trivy pulls the database
from `mirror.gcr.io/aquasec/trivy-db:2` first and
`ghcr.io/aquasecurity/trivy-db:2` second, and the ghcr.io copy has a
history of `TOOMANYREQUESTS` refusals for CI fleets
([trivy#7668](https://github.com/aquasecurity/trivy/discussions/7668)).
So the root workflow's `trivy-db` job downloads the database once,
and every job that scans restores it from the Actions cache.

The facts the design rests on, checked on 2026-10-31:

- The database layer on ghcr.io was 137,670,336 bytes gzipped, and
  `trivy.db` was 1,674,162,176 bytes unpacked. Its `metadata.json`
  held `UpdatedAt` 2026-10-31T13:09Z and `NextUpdate` 24 hours later.
  The trivy-db repository builds the database every 6 hours
  ([trivy-db](https://github.com/aquasecurity/trivy-db)).
- Trivy downloads nothing when the database in its cache directory is
  current: `NeedsUpdate` in `pkg/db/db.go` of v0.76.1 returns false
  while the time is before `NextUpdate`, or within an hour of
  `DownloadedAt`. `trivy image --download-db-only` writes
  `DownloadedAt`, so a restored copy needs no `--skip-db-update`. A
  copy pulled with `oras`, as in trivy-action's README, has no
  `DownloadedAt`, and Trivy downloads it again unless the scan skips
  the update.
- The default cache directory is `os.UserCacheDir()` plus `trivy`,
  which is `~/.cache/trivy` on a runner (`pkg/cache/dir.go`), and the
  database is under `db/` there.
- The Java database downloads only when a scan finds a JAR file
  (`pkg/javadb/client.go` updates it in `NewClient`), and the checks
  bundle only for a misconfiguration scan, so `--scanners vuln` on an
  image without JAR files reads neither
  ([databases](https://trivy.dev/latest/docs/configuration/db/),
  [cache](https://trivy.dev/latest/docs/configuration/cache/)).
- trivy-action caches the cache directory under
  `cache-trivy-<YYYY-MM-DD>` with the restore key `cache-trivy-`, and
  its README suggests a scheduled job on the default branch that
  saves the database for the scans to restore
  ([trivy-action](https://github.com/aquasecurity/trivy-action)).
  setup-trivy caches only the binary
  ([setup-trivy](https://github.com/aquasecurity/setup-trivy)).
- A run can restore a cache entry of its own branch or of the default
  branch, an entry never changes, and GitHub deletes an entry that no
  run read for 7 days, within a limit of 10 GB for the repository
  ([dependency caching](https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching)).

The `trivy-db` job follows trivy-action's key, with no schedule,
because a timer that reads state again is a defect. The plan runs the
job only when the run scans an image: the `trivy` job, or a publish of
a component with images (`ScansImages` in `ci/rescan.go`, and the plan
job's `trivy-db` output). The job looks up `trivy-db-<day>` without
downloading it. On a miss, it installs Trivy, runs `trivy image
--download-db-only`, saves `~/.cache/trivy/db` under the key, and
deletes every older `trivy-db-` entry with `gh cache delete`, so the
cache holds one entry of about 140 MB, not one for each of the last 7
days. The `trivy` job and each publish call that scans images need the
`trivy-db` job, and each restores the key of its own day, or the
newest `trivy-db-` entry, before it installs Trivy. So when the cache works, a run downloads
the database at most once, and only the first scanning run of a day
downloads it.

An artifact was the alternative. It would not use the cache's 10 GB,
but each run would upload the database again and download nothing
from an earlier run, and the cache already shares one entry with every
run of the day. Every step that reads or writes the cache continues on
an error. When a restore fails, or restores a database that is no
longer current, Trivy downloads its own before the first scan, so the
cache saves downloads and never stops a scan.

Trivy's findings change only when it runs, unlike Dependabot's. An
advisory against a Debian package in an image that no push touches
shows in the Security tab at that image's next publish. The plan
accepts that gap, because a scan on a schedule is a timer that reads
state again to find a change, which `AGENTS.md` calls a defect.

## Later work

- An SBOM for each image, with `--sbom=true` on the bake, and for the
  OS, from a generator that reads its `VERSION` files, published with
  each release.
- A check that fails the build on a finding that a bump can reach.
  The rule from the evidence above: a Go finding is fixable when it has
  a `fixed_version`, and a Rust finding when `cargo update --precise`
  resolves to it.
- Renovate, if Dependabot's per-directory pull requests cost too much
  to finish by hand, because a Renovate group moves every copy of a
  dependency in one pull request.
- A Trivy misconfiguration scan (`trivy fs --scanners misconfig`) of
  each component's Dockerfiles and `deploy/` manifests, on the same
  rule as the other jobs: the plan job runs it only for a component
  whose Dockerfile or manifests changed. The image scan reads packages
  only, so a pod that runs as root or a manifest with no
  `securityContext` raises no alert today. The first scan will report
  many findings, so it needs its own pass to fix or dismiss them.
- OpenSSF Scorecard, which grades supply-chain practices such as
  pinned actions, token permissions, and branch protection, and
  reports each failed check to code scanning. Several checks conflict
  with choices `liken` made on purpose: actions are pinned by tag,
  not by commit, and one maintainer pushes to `main` with no review.
  So the work starts with a decision on which checks `liken` accepts,
  and it runs on a push to `main`, not on a weekly schedule.

## Order

1. Add the CodeQL job to `ci/`'s templates, with its rule for which
   files each language reads, and run `make workflows`.
2. Add the Trivy step to the publish job's template, and run
   `make workflows`.
3. Turn on Dependabot security updates with `.github/dependabot.yml`,
   and add the `ci/` check that every module and workspace is listed.
