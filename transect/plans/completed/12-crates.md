# 12: Crates

transect is one crate of about 105,000 lines.  CI's tests job compiles
it as one unit and peaks at 5.0 GB of its 6 GB limit.  This plan splits
it into a Cargo workspace, in two stages:

1. Untangle the references between modules inside the one crate, so
   that every module names only modules in its own crate or a lower
   one.  `src/crates.rs` held the table below and a test that failed on
   a reference against the order, until stage 2 removed it.
2. Cut the crates: move each row's modules into its own crate, with
   `crate::x` becoming `transect_y::x`.  With stage 1 done this is
   mechanical.

## The crates

From the bottom up.  A module may name only modules in its own row or
a row above it in this table.  Modules keep their names inside each
crate.

| Crate | Modules |
|---|---|
| transect-frame | frame |
| transect-graph | edges, links, selector, pods, dns, untrusted, upstream, environment |
| transect-resources | resources, knowledge, intent, layers |
| transect-cluster | cluster, connect, ownership, workloads |
| transect-observe | health, logs, alerts, metrics, usage, prometheus |
| transect-ground | machines, devices, storage, volumes, outside, loss, placement, guards |
| transect-supply | images, forge, gitops, changed, findings, vulnerabilities |
| transect-network | policy, entry, addresses |
| transect-map | map, cards, objects, lenses, search |
| transect-access | access |
| transect (binary) | web, main |

Each crate keeps its own facts, cards, and templates together.  This
is grouping by domain: a crate is not a model or a view layer.  No
crate is named common, shared, utils, core, or helpers, and nothing
moves into transect-graph only because it is the bottom.

## What the test counts

`src/crates.rs` reads every file under `src/` and counts, as a
reference from the file's top-level module to another:

- every `crate::x` path in Rust, with `crate::{a, b}` read as both;
- every template path after `{% import %}`, `{% include %}`, and
  `{% extends %}` in a template, and after `path =` in a template
  derive.  askama resolves these across crates through `dirs` in each
  crate's `askama.toml`, so they are dependencies like Rust imports.

Test code counts the same as other code.  A test in a low crate cannot
reach anything in a higher one, and the fixture cluster in
`src/web/fixture/` builds a `web::snapshot::Snapshot`, so it reads every
domain.  It cannot become a dev-dependency crate of the low crates:
Cargo allows the cycle, but the low crate's test build would then see
two copies of its own types, the one it compiles and the one the
fixture links.  So a test that needs the whole fixture cluster belongs
in the binary, beside the pages it renders, and a low crate's tests
build their objects with their own fixtures.  The tests that read the
fixture for gitops and vulnerabilities live in `web::fixture::tests`.

`UPWARD` in `src/crates.rs` lists the references that still go up, as
(from, to) pairs.  The test fails on an upward reference not in the
list and on a listed pair that no longer occurs.  The list reaching
empty is the end of stage 1.

## Causes

As found when stage 1 began; "Where it stands" says what each
became.

- **Small types defined in a high module.**  `edges` reads
  `intent::Provenance`, `intent` and `links` read `ownership::Plurals`,
  and nine modules read `cards::Reference` and `cards::Named`.
- **`resources` parses every kind.**  It reads access, logs, metrics,
  images, forge, entry, devices, outside, layers, knowledge, and
  intent, and they read it back.  The document and condition types
  stay low, each domain owns the parser for its own kind, and the
  loader that knows every kind moves to the lowest place that reads
  them all.
- **`links` builds every URL.**  It reads `gitops`, `objects`,
  `knowledge`, and `ownership`, while nearly every module reads it.
- **The page frame.**  `web/layout.html` is extended by full pages in
  map, access, objects, vulnerabilities, outside, changed, lenses,
  search, and resources.  `map/crumbs.html` and `lenses/tabs.html` are
  used by access, objects, vulnerabilities, outside, and search, and
  `cards/workload.html`, `cards/namespace.html`, and `objects/card.html`
  by access and vulnerabilities.
- **Upward calls,** such as `access` into `map`, `knowledge` into
  `entry`, `addresses`, `gitops`, `cluster`, and `cards`, and `cluster`
  into `gitops`, `devices`, and `storage`.

### The page frame

The frame is the module `frame`, in its own crate, `transect-frame`, at
the bottom of the table: `frame/layout.html`, `frame/crumbs.html`,
`frame/tabs.html`, the brand under `src/frame/brand`, and the Rust
types the two small templates read, `frame::crumbs::Crumb` and
`frame::tabs::{Lens, Tabs}`.  `layout.html` reads no field of its page;
it only declares blocks.  The frame names nothing else in transect, so
`Tabs::namespace` and `Tabs::machine` take the page's path from their
callers, which build it with `links`.

The other choice was to move every full page up into the binary, where
`layout.html` was; that puts the access, vulnerabilities, ground, and
map pages back into the one compile unit this split exists to shrink,
and pulls each page's words away from its domain.

The binary serves the assets from `web::assets`, which names each
file by its path under `src/`, so the brand is served from
`frame/brand/` at the same `/static/brand/` URLs.  The stylesheets
keep their files and URLs: `web/layout.css`, which styles the
masthead, and `lenses/lenses.css`, which styles the tabs, stay where
they are, since moving a file changes the URL every page links.
Stage 2 gives `web::assets` the frame crate's directory for the brand.

## What stage 2 must touch

These paths are read through `CARGO_MANIFEST_DIR`, which becomes each
crate's own directory after the cut:

- `src/web/assets.rs`: in release, `include_bytes!` of
  `CARGO_MANIFEST_DIR/src/<source>`, and in debug the same path read
  from disk; the sources include each module's CSS and the brand
  under `frame/brand`, which will sit in other crates.
- `src/cluster/grants.rs` reads `deploy/rbac.yaml`.
- `src/web/golden/mod.rs` reads `src/web/golden/pages`.
- `src/web/loaded/examples.rs` reads `examples/`.
- `src/crates.rs` reads `src/`; after the cut its test has no work
  left, and the crate graph in the workspace's manifests holds the
  order.

Each crate with templates needs its own `askama.toml` with
`dirs = ["src", ...]` listing the `src/` of each lower crate whose
templates it names.  A template struct from another crate rendered
with `{{ card }}` needs `|safe`.

## Order of work

One cause per commit, simplest first:

1. The small types: `Provenance`, `Plurals`, `Reference` and `Named`.
   Done.
2. The upward calls that are one function or one test fixture each.
   Done.
3. The page frame.  Done.
4. `resources`.  Done.
5. `links`.  Done.
6. `cluster`'s builders of other domains.  Done.
7. The tests that read the fixture cluster.  Done.
8. The pages that draw map's cards and the section view's drawing,
   in access.  Done by giving `access` a row above map.

## Where it stands

Both stages are done.  Stage 1 emptied the list, and stage 2 cut every
row of the table into its own crate; "Stage 2" says how.

The list started at 60 pairs and is empty.  The table gained
transect-frame at the bottom, and transect-access became two rows:
transect-network below map and transect-access above it.

Moved so far, each to the module whose domain it is:

- `Provenance`, `Stated`, `Way`, and `provenance.html` from intent to
  `edges::provenance`, since an edge records where it came from.
- `Plurals` from ownership, and `ClusterScoped` from ownership, to
  edges, beside `Object`: both are what discovery says of each kind.
- `Reference` and `Named` from cards to `links::reference`: a name and
  the page it opens.
- `Claim`, a claim by namespace and name, from storage to
  `edges::claim`, beside `Object` and `Device`.
- The test builder `device` from the devices tests to
  `edges::fixtures`, beside `Device`.
- The http base URL check from `images::mirror` to
  `upstream::http_base`, which the mirror parser now calls.
- The fixture passes the default cluster domain to
  `LogTails::offline`.
- `age` from workloads to `knowledge::age`, beside the printer columns
  that print a date column with it.
- A custom resource's health (`Deciding`, `condition`, `reported`,
  `resting`) from knowledge to `health::known`.
- `Objects`, the workload stores health reads, from health to
  workloads.
- `GitOpsFields::paths` in knowledge, in place of `gitops::paths`, and
  `DynamicObject` in place of the `cluster::Custom` alias.
- The page frame to `frame`: see "The page frame".
- `resources` reads the directory into a `Directory` (the files and
  the overlays) and hands each document of `GROUP` to a `Sorts`.  The
  binary's `web::loaded::Loaded` is that `Sorts`, with the dispatch by
  kind and its tests and the examples test.  Each low reader takes
  its own field through `resources::Field<T>`, a watch of one field of
  whatever the binary loads, built in `main.rs`; `intent::unattached`
  takes the overlays and Intents, and each `off_line` takes the
  `Directory`.  A new kind of `GROUP` is a parser in its domain and an
  arm in `web/loaded.rs`.
- The URL names of the built-in kinds and of the top owners' kinds to
  `links::kinds`, from `objects::kinds` and `ownership/resource.rs`.
  `objects::kinds` keeps how a page reads each kind, in a table of its
  own, with a test that both tables name the same kinds.
  `links::Pages` to `map::pages`.
- `Storage::read` and `Devices::read` build those from the cluster's
  stores in their own domains.  `Cluster::ownership` hands the marked
  objects to a closure, so the snapshot builds GitOps's `Applied`, and
  `Watch`, how far a kind's watch has come, moves from gitops to
  cluster.  `cluster::discovery` and `cluster::devices` are public for
  `Devices::read`.
- Resolving what a kind knowledge relation or a guessed clue names to
  `addresses::relations` and `addresses::guess`; knowledge keeps the
  relations, the clues, and the edges they draw.
- The fixture-reading tests of gitops and vulnerabilities to
  `web::fixture::tests`.
- The quick search entries of the vulnerabilities to
  `search::vulnerabilities`.
- The loss cascade tests build their own Ingresses.  The map's
  sidebar wraps the loss sidebar in its aside and adds what to know now
  below a real loss, so the loss template includes nothing of map's.
- The ground page to `map::ground`, and a workload's vulnerabilities
  tab to `map::vulnerabilities`, since each draws map's or objects'
  boxes and cards.

Nothing is left.  The last three pairs, `access` to `cards`, `map`,
and `objects`, went with a change to the table and no change to the
code.  The access lens draws map's cards and reuses the section
view's drawing, and those cards read `addresses::Depends` and
`policy::Calls`, so the four modules could not share a row on either
side of map.  Only `access` itself and the binary name `access`, so it
has a row of its own above map, and `policy`, `entry`, and `addresses`
are transect-network, below map.

## Stage 2

The workspace manifest is at the root, and each crate is a directory
under `crates/` named for the crate, with the binary in
`crates/transect`.  Cut so far, one commit each, from the bottom:
transect-frame, transect-graph, transect-resources, transect-cluster,
transect-observe, transect-ground, transect-supply, transect-network,
transect-map, and transect-access.  `crates/transect/src/crates.rs`
and its test are gone.  Cargo's dependency graph and each crate's
`askama.toml` enforce the order now.  A crate's Rust can name only the
crates its manifest depends on, and Cargo refuses a cycle among them.
A template that a crate derives can import, include, or extend only
templates under the `src/` directories its `askama.toml` lists, which
are its own and those of lower crates.  One case goes unchecked: a
template that a higher crate includes resolves through that higher
crate's directories, so an include from transect-ground's templates
of a template in transect-map would compile.  Review catches that one.

Measured on a workstation, with nothing else building, from the
commit before the workspace (one crate) and from the finished cut:

| | One crate | Workspace |
|---|---|---|
| Clean `cargo build --release` | 163 s | 192 s |
| Clean `cargo test --no-run` | 153 s | 156 s |
| Debug rebuild after touching a map template | 10.2 s | 7.3 s |
| Debug rebuild after touching a file in transect-graph | 10.5 s | 10.8 s |
| Largest single rustc, `cargo clippy --tests -j 4` | 3.77 GB | 2.41 GB |
| Every rustc at once, same command, sampled each second | 3.77 GB | 4.37 GB |

The memory that CI's 6 GB limit counts is the last row.  The one
crate's peak is a single rustc; the workspace's is several crates
checked side by side, each smaller.  A clean release build took 29 s
longer, and this plan did not measure why.

What the cut changed beyond the paths:

- Rust keeps a type's inherent methods in the type's crate.
  `Device::short` and `Device::full` moved from devices to
  `edges::device`, beside `Device`.  `Vulnerabilities::drawn` became
  `map::vulnerabilities::drawn`, and the binary sets a workload page's
  `manages` field where it renders the page.
- A template's Rust paths expand in the crate that derives the
  template, which can be a higher crate that includes it.  So templates
  name each module by its crate, as `transect_graph::links::machine`,
  and transect-map declares `extern crate self as transect_map` for
  its own templates' paths.
- The binary's `askama.toml` lists every lower crate with templates,
  since an include in a lower crate's template resolves through the
  directories of the crate that derives the page.
- A test builder that another crate's tests read sits behind its
  crate's `fixtures` feature, as `#[cfg(any(test, feature =
  "fixtures"))]`, and each crate above turns the feature on in its
  dev-dependencies.  A builder that only its own crate's tests read
  stays `#[cfg(test)]`.
- `web::assets` names each file by its crate's directory under
  `crates/`, so the URLs stay the same and each file stays beside its
  module.
- The release profile names each workspace crate at opt-level 1.
  `package."*"` would have done it in one line, but it would also
  raise the build scripts and proc macros from build-override's
  opt-level 0 to 3.
- Each crate lists only the dependencies its code names, and one that
  only its tests name is a dev-dependency.  A `fixtures`
  dev-dependency stays only where `cargo check -p <crate> --tests`
  fails without it, since the workspace build unifies features and
  hides a missing one.  That check found transect-supply's `fixtures`
  feature reading transect-resources' builders without turning them
  on; it turns them on now.  transect-resources' `fixtures` turns on
  its optional `anyhow`, which only its builders read.
- The Dockerfile builds the dependencies against a skeleton that a
  stage of its own makes from every crate's manifest and a stub
  source, so a crate added under `crates/` needs no change to it.
  BuildKit keys the copy of the skeleton by its contents, so a
  source-only change reruns the skeleton stage and reuses the
  dependency layer.
