# No check finds known vulnerabilities

Open design question. CI runs no vulnerability scanner, so a pin with a
published advisory stays in the tree until `make versions` reports the
pin as `behind` and someone bumps it. `make versions` compares versions,
not advisories, so it gives no sign of which bump fixes a vulnerability.

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

## The rule

A finding that a dependency bump can fix fails the build. A finding
that no bump can fix only warns.

## What the rule needs

None of the tools can apply the rule on its own. `govulncheck`,
`cargo audit`, `cargo deny`, and `osv-scanner` each exit non-zero for
every finding, so the check needs a step that reads the tool's JSON and
sorts each finding.

- **Go.** A finding is fixable when `govulncheck` reports a
  `fixed_version`. Go's minimum version selection takes the highest
  version that any requirement names, so a `require` line in `go.mod`
  reaches a fix even for an indirect dependency. A finding in the
  standard library is fixable by a Go toolchain bump.
- **Rust.** A patched version is not enough. `media-operator` takes
  `lru` 0.16.4 through `iced_wgpu` and `cryoglyph`, which requires
  `lru = "^0.16"`, and the fix is in 0.18.2. No change in this
  repository reaches it until `iced` releases. `rustls` 0.23.46 is
  semver-compatible with what `ureq` requires, so `cargo update` reaches
  it. A Rust finding is fixable when the lock file can resolve to a
  patched version, which `cargo update --precise <fixed> --dry-run`
  answers for each finding.

## The decisions it needs

- **Fixable from the tree, or fixable at all.** The check can test
  whether the lock file can resolve to the fix, as above. The other
  choice is to fail on any advisory that has a patched version and keep
  each blocked one in a `deny.toml` ignore with its reason. That list
  needs a person to remove each entry when upstream moves.
- **When the check runs.** `ci/` runs a component's jobs only when the
  component changes, but an advisory arrives with no commit. A job in
  each `package.toml` fails the next push that touches the component,
  not the push that caused the finding, and reports nothing before then.
  A daily workflow reads the advisory databases again to find a change,
  which is the timer that `AGENTS.md` calls a defect, because the
  databases offer no subscription. Dependabot alerts in the `liken.sh/`
  Terraform are the one source that GitHub sends when an advisory
  matches, but they fail no build.
- **The images.** These tools read `go.mod` and `Cargo.lock` only. The
  Debian packages in the bases, `eclipse-mosquitto`, `busybox`, and the
  rest of the images need an image scanner such as `trivy` or `grype`.
