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
run for nothing new.

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

Each job uploads its results under the category
`codeql/<component>/<language>`. Code scanning closes an alert only
when a later upload in the same category no longer reports it, so a
category per component keeps the alerts of each component until that
component's next analysis, and an analysis of one component does not
close the alerts of another.

## Trivy

Trivy reads the operating system packages and the language packages
inside an image. It runs in each component's publish job, on each
image that the job has just pushed, by digest, so it reads the bytes
that a cluster pulls. Its SARIF goes to code scanning under the
category `trivy/<image>`. A component that does not publish starts no
Trivy scan, and a pinned base, which publishes only when its tag is
new, is scanned once for each tag.

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

## Order

1. Add the CodeQL job to `ci/`'s templates, with its rule for which
   files each language reads, and run `make workflows`.
2. Add the Trivy step to the publish job's template, and run
   `make workflows`.
3. Turn on Dependabot security updates with `.github/dependabot.yml`,
   and add the `ci/` check that every module and workspace is listed.
