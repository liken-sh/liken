# Working on transect

This directory is `transect`, a live, read-only map of a Kubernetes
cluster: its layers, the relationships between its parts, and their
health.  It is a component of the `liken` repository.  Read
[plans/00-design.md](plans/00-design.md) before you change anything,
because it records every decision made so far.

@../brand/voice.md

The voice rules in that file govern all prose in this directory,
comments included.

## Privacy

The "Privacy" rule in the root `AGENTS.md` applies here.  Tracked files
name no real cluster: no host names, node names, namespaces, or
addresses.  `liken`, its operators, and their custom resources are fine
to name.  Examples use generic stand-ins such as "a database", "a media
server", or "a USB radio".

Everything specific to one cluster is in `local/` in this directory,
which git ignores: that cluster's resources for `transect`, its design
drafts, and notes on how to reach it.  Read `local/README.md` when it
exists.  Knowledge that a public project would ship, with nothing of
one cluster in it, goes in `examples/`, one directory per project,
which a test loads.

## How it is built

- The server is Rust, on kube-rs for watches and caches and axum for
  HTTP.
- The server renders the pages, HTMX swaps fragments, and server-sent
  events push changes.  It is not a single-page application.  Each
  elevation has its own URL.
- The pages are dark by default, calm, and gray where things are
  healthy.
- The runtime makes no language model calls.  Every sentence is a
  template or stored text; see "Deterministic at runtime" in the
  design.
- `transect` writes nothing to a cluster except status on its own
  resources.

## Where things live

`transect` is a Cargo workspace with one directory per crate under
`crates/`, from the page frame at the bottom to the binary at the top:
`transect-frame`, `transect-graph`, `transect-resources`,
`transect-cluster`, `transect-observe`, `transect-ground`,
`transect-supply`, `transect-network`, `transect-map`,
`transect-access`, and `crates/transect`, which contains the server,
the pages' routes, the fixture cluster, and `main.rs`.  A crate names
only the crates below it, and its `askama.toml` lists the `src/` of
each lower crate whose templates it uses;
[plans/completed/12-crates.md](plans/completed/12-crates.md) gives the
reason for each module's place.  A crate lists only the dependencies
its code names, and one that only its tests name is a dev-dependency.
Each crate keeps its templates, styles, and tests beside its code.  A
test builder that another crate's tests read is behind its crate's
`fixtures` feature, which the crates above turn on in their
dev-dependencies.  `deploy/`, `examples/`, `plans/`, and `scripts/`
stay at the top of this directory.  `cargo build`, `cargo test`, and
`cargo clippy` in this directory cover every crate.

No check in CI caps a file's length, so keep each source file at 500
lines or fewer.  When one nears that, move a domain out of it, or split
a test file into focused pieces.

## The development loop

`transect` runs on a workstation against a real cluster, with the
workstation's kubeconfig, a Prometheus URL, and its own resources and
overlays from local YAML:

```bash
cargo run -- --kubeconfig ~/.kube/config \
  --prometheus http://localhost:9090 \
  --resources ./local/resources/
```

Add the flags a change needs (`cargo run -- --help` lists them all):

- `--alertmanager <url>`: read the firing alerts from this
  Alertmanager, such as a port-forward; `--alertmanager-url-external`
  sets where each alert's link opens, when a browser reaches it at
  another address.
- `--registry-auth <config.json>`: registry credentials for reading
  image labels, from a Docker `config.json`.
- `--registry-mirror <host>=<mirror>`: read a registry host's labels
  from a mirror; a `RegistryMirror` resource does the same.
- `--log-source-url <url>`: reach the log store at this URL, such as a
  port-forward, in place of the address its `LogSource` gives.
- `--secret-annotations`: read intent annotations on Secrets
  themselves, for a kubeconfig that may read Secrets anyway.
- `--pretend-not-ready <machine>`: draw the loss of a machine as if it
  were down, without touching it.  This flag exists only in debug
  builds.
- `--fixture`: serve the small cluster the tests build, from
  `crates/transect/src/web/fixture/`, with no kubeconfig, and contact
  nothing on the network.  Work on templates, CSS, and layout against
  it, so each restart does not list a real cluster again, and check the
  result against the cluster once at the end.  This flag exists only in
  debug builds.

Change the code or a template, rebuild, reload the page, and look.  A
change to a file under `--resources` reloads without a restart, and
`/resources` lists every resource and overlay with its conditions, so
a typo shows there.  The loop needs no image and no deploy.  Prove each
change by running it against the cluster, and keep the unit tests next
to the code they test.

`crates/transect/src/web/golden/pages/` contains the visible text of
the pages whose text uses the project knowledge that resources supply,
drawn from the fixture.  When a change to their words is meant, write
them again with `TRANSECT_GOLDEN=rewrite cargo test golden` and read
the diff.  `scripts/side-by-side --old <binary> --new <binary> --
<flags>` runs two builds against the cluster at once and diffs their
pages' text with the numbers masked; `scripts/side-by-side --help`
gives the details.

## CI and the image

`ci/` at the top of the repository writes
`.github/workflows/component-transect.yaml` from `package.toml`, and
`make workflows` at the top writes it again.  Edit `package.toml` or
the templates in `ci/templates/`, not the workflow, because the graph
job of `.github/workflows/ci.yaml` fails when the committed workflow
differs from what `ci/` writes.

The `rust` job runs `make test`, the target a workstation runs.  It
checks the format with `cargo fmt --check`, then checks the two htmx
files in `crates/transect/src/web/static/` against `htmx.sha256`, so a
file that differs from its pin fails.  Then clippy runs twice with
warnings denied: once on the code as it ships, and once with the
tests.  With `--all-targets`, cargo runs both checks of a crate at
once, beside the checks of other crates, and the memory of every rustc
that runs at that moment counts against the runner's memory, so two
runs keep the two checks of each crate apart.  Last, `cargo llvm-cov`
runs the tests of every crate and fails when line coverage is below
`COVERAGE_FLOOR` in the `Makefile`, which is 88 percent.  Raise the
floor when the tests earn it; never lower it.  The same run writes
`coverage-transect.xml`, which `make coverage-report` draws as one
page.

The `prek` job runs the hooks of `.pre-commit-config.yaml` on every
file in `transect/`: the hygiene checks and `shellcheck`.  It skips
`cargo-fmt`, because the `rust` job runs the same check.  The
`codeql-rust` job, which `ci/codeql.go` derives, analyzes the Rust
sources with CodeQL.

`ci pins`, which the graph job runs, holds each crate to one version
across every `Cargo.toml` in the repository, and holds the channel of
`rust-toolchain.toml` to the Rust of the `rust` image in
`toolchains/images.toml`.  So a crate bump or a Rust bump changes every
copy in one commit.  It also checks `htmx.sha256` against `pin.toml`
beside it, and that the `Dockerfile` builds with `cargo auditable
build`.  `crates/transect/src/web/static/latest.sh` reports the newest
release of each htmx file, and `latest.sh --bump` writes the files,
their sums, and their pins.

`docker buildx bake transect --load` at the top of the repository
builds the image into the local Docker daemon, as
`ghcr.io/liken-sh/transect:check` unless `VERSION` is set.  The
`Dockerfile` builds on the `rust` image and leaves `rust-toolchain.toml`
out, because that image has the same rustc, which `ci pins` checks, and
without the file rustup does not download clippy and rustfmt on each
build.  Every build runs through `cargo auditable build`, which records
the crate graph in the binary, so syft and Trivy list the crates from
the image.  The dependencies compile in a layer of their own, against a
skeleton of every crate's manifest, `Cargo.lock`, and an empty source
for each crate.  So a cached build compiles them again only when a
manifest or `Cargo.lock` changes, and a crate added under `crates/`
needs no change to the `Dockerfile`.  The binary links glibc, so
`transect-closure.sh` collects the binary and every library that the
loader resolves for it, from a Debian image at `vulkan`'s snapshot
date, with `vulkan/closure.sh`, and the last stage copies that tree
onto `scratch`.  The image also holds the CA bundle of the `trust`
component, the license notices of the Rust standard library, of each
linked crate, of each Debian package, of the fonts, and of htmx, and a
CycloneDX record of the two htmx files for the image's SBOM.  It runs
as user 65534, the user that `deploy/transect.yaml` sets, and it has no
shell.

`smoke/transect.sh <image>` is the check that the images job runs on
the image it builds.  The runner has no cluster, so the script does not
start the server.  It checks, through `notices/check.sh`, that the
image holds each notice and the CA bundle, checks that the image runs
as `65534:65534`, and runs the binary with `--help`, which fails when
the loader cannot find a library.

## Where to start

`transect` as it is now is a proof of concept, and the maintainer
intends to design its ideas again.  No plan for new work is open.  The
plans in `plans/completed/` record how each part was built, in order:
plans 01 through 06 built the elevations, every edge the API gives,
image provenance, a lost machine, kind knowledge, intent, log sources,
lens tabs, quick search, and the machine, ground, and volume pages.
Plan 07 ran `transect` in a cluster.  Plan 08 moved the knowledge of
particular projects out of the code into resources, plan 09 built the
access lens, plan 10 the vulnerabilities pages, and plan 12 split the
crate into the workspace that "Where things live" describes.

`plans/open-problems/` holds the defects that a test reproduced in the
code as it is.  Write a plan before you build anything new.
