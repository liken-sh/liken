# The repository and its builds

Plan 01. The skeleton: everything that must exist before the first
feature, built small so each later plan changes less. At the end of this
plan the operator runs and reconciles nothing, the media browser opens a
window and draws nothing, every image builds in CI, and the local
harness has a home.

## The problem

The design names four kinds of artifact: a Go operator, Go scanner
images, a Rust media browser image, and a Corrosion sidecar image. It
also names a release channel, a documentation site, and a local harness.
Each has a first-time cost that has nothing to do with libraries:
toolchains, caches, image bases, workflows. Paying that cost inside the
first feature plan makes that plan large and its review hard. So the
skeleton is its own plan, and it delivers builds.

## The contract

The repository follows `media-operator`'s shape wherever the two share a
concern, so a person who reads one can read the other.

- A Go module at the root for the operator. It builds the way
  `media-operator` builds: its own API client and watches against the
  API server, one binary with modes, no controller framework. In this
  plan the operator starts, connects, and logs that it has no `Library`
  to reconcile.
- A Rust crate for the media browser in its own directory with its own
  `Cargo.toml`. In this plan it opens a Wayland window, fills it with
  the theme's black, and exits on `q`. It has the harness flags the
  later plans need from the start: a scripted timeline of key events,
  frame capture to a directory, statistics to a file, and a quit-after
  timer. Plan 05 depends on them.
- Images. One for the operator on `scratch`, as `media-operator`'s is.
  One per scanner kind, added by the plan that adds the kind. One for
  the media browser on a distribution base, because Iced needs a Wayland
  and a Vulkan or EGL stack, as `media-operator`'s player image needs
  `mpv`'s. One for the Corrosion sidecar, built from a pinned Corrosion
  commit with the patches the catalog plan names.
- The release workflow, triggered by a tag, versioned by CalVer, and
  published to the same kind of channel the other operators use. The
  Rust build is the slow part: 80 s for 205 crates on a 22-thread
  laptop, and several minutes on a hosted runner. Almost all of it is
  dependencies, which change only when the lock file changes. So the
  workflow caches the cargo registry and the build tree, keyed on the
  lock file, and the media browser's Dockerfile builds dependencies in a
  layer of their own that the image cache keeps between runs.
- CI runs the Go tests with a coverage gate, `clippy` and the Rust
  tests, and a build of every image.
- `AGENTS.md`, with `CLAUDE.md` as a symlink to it, the brand theme as a
  submodule, and the documentation site scaffold. The reference pages
  generate from the CRD schemas, as the other operators' sites do.
- A `local/` directory in the shape of `media-operator/local/`: one
  script per thing a person can run on a workstation without a cluster.
  This plan adds the first, which runs the media browser in a window
  with the harness flags. Later plans add a three-agent Corrosion
  cluster, a scanner against a local directory of movies, and a media
  browser against that catalog, so the whole read path runs on a laptop.

## What was set aside

A controller framework. `media-operator` reconciles with a small
hand-written client and watches. The two operators read alike if this
one does the same, and nothing in this design needs what a framework
adds.

One Dockerfile for everything. The media browser's base and the
scanners' bases share nothing with the operator's `scratch`, and one
file with four stages is read four times as often as it is changed.

## Proof

CI is green on the first push: every image builds, both test suites run,
and a tag publishes a release with every artifact. On a workstation, the
`local/` script opens the media browser's empty window under the desktop
compositor and again under `cage` on the headless backend, and the
capture flag writes a black 1920x1080 frame.
