# 69. One repository builds every component

Milestone 69. Proposed 2026-09-25. Revised 2026-09-28: the revision
moves the operators, the drivers, `brand`, and `.agents` into this
repository, and adds one release tag for all of them. Built
2026-09-29. The second stage moved to plan 72 on the same day.

Today `liken` downloads most of the programs in its image as binaries
that other projects built, and its operators and drivers live in
eleven other repositories that copy each other's code. This milestone
moves every component that the project ships into this repository. A
component is a directory with a `package.toml`, and CI reads those
files into one dependency graph.

The work ran in two stages, and this plan holds the first. The first
stage moves the repositories together and builds the component graph.
The second stage moves the OS's vendored domains to stagex builds, so
that `liken` builds every program in its release artifacts from
source. The build tools come from stagex, and each build is pinned
and signed. The second stage is
[plan 72](../72-the-os-builds-from-source.md).

A component takes its version in one of two ways. A **pinned**
component, such as the kernel or `mpv`, declares an upstream version
and a revision, and it builds only when that revision is not
published yet. A **tracked** component, such as an operator or the
OS itself, takes the repository's CalVer release tag, and it builds
only when its files or a dependency changed since its last published
version. Everything else is shared: the directory, the file, the
graph, the publish steps, and the attestations.

## The problem

### The split repositories

On 2026-09-28 the project shipped from twelve repositories: `liken`,
nine operator and driver repositories, `brand`, and `.agents`. A
review of the three days before that date found that most of the
work went into keeping copies in agreement:

- Six operators each carry their own `objectcache.go`, and every
  operator carries its own `apiclient.go`, `watch.go`, `cdi.go`, and
  a `ci.yaml` of about 300 lines. `liken` has a seventh cache in
  `kubernetes/informer`. On 2026-09-27 one fix to the cache landed
  as four separate commits with four separate tests.
- `media-operator` and `library-operator` carry the same `mqtt.go`,
  with a difference of one comment, and near copies of `leader.go`.
  `equipment-operator` carries a third copy of each.
- Twelve repositories pin `brand` through a git submodule. The pin
  in `liken/go.mod` was one month older than the pin in
  `liken/docs`.
- `display-operator`'s release builds the `vulkan`, `vaapi`,
  `ffmpeg`, and `mpv` base images under its own version. The
  consumers pin those images by hand, and the pins differed:
  `library-operator` used `ffmpeg:2026.09.11-001` and
  `media-operator` used `ffmpeg:2026.09.16-001`.
- A cluster that follows a development build must pin the manifests
  to the full 40-character commit sha, because `git fetch` by sha
  needs all forty characters, and the image to the build's version.

The git history of the ten repositories that release gives the size
of the cost. The counts below cover `liken`, the seven operators, and
the two CSI drivers, and they were taken on 2026-09-28. A release
session is a group of release tags with less than two hours between
one tag and the next.

| Window | Release tags | Sessions | Sessions that tag more than one repository | Tags in those sessions |
| --- | --- | --- | --- | --- |
| 2026-07-11 to 2026-09-28 | 445 | 114 | 38 (33%) | 65% |
| 2026-08-29 to 2026-09-28 | 255 | 59 | 27 (45%) | 73% |
| 2026-09-21 to 2026-09-28 | 65 | 11 | 7 (63%) | 93% |

- In the 30 days to 2026-09-28, four sessions tagged `liken` and all
  nine other repositories.
- In the same 30 days, 11 sessions tagged `liken`, and 9 of them also
  tagged operators or drivers.
- `media-operator` and `library-operator` released in the same
  session 22 times in those 30 days.

The grouping by time counts more sessions than the dependencies
require. One person often works across several repositories in one
sitting, so some of the tags are together by chance. The tags alone
cannot separate the two cases.

The commits show the duplicated work more directly. In the same 30
days, 163 of 999 commits (16%) have a subject line that is identical
to a commit in another repository. A fix whose subject is worded
differently in each repository does not match, so 16% is a lower
bound. The first operator repositories started on 2026-08-16, in
week 33. Before then only `liken` existed, and the rate was 0%. Since
then it has been between 10% and 25% in every full week:

| ISO week | Commits | Subject repeated in another repository |
| --- | --- | --- |
| 2026-W33 | 55 | 6 (10%) |
| 2026-W34 | 183 | 21 (11%) |
| 2026-W35 | 171 | 41 (23%) |
| 2026-W36 | 291 | 45 (15%) |
| 2026-W37 | 188 | 33 (17%) |
| 2026-W38 | 202 | 51 (25%) |
| 2026-W39 | 239 | 29 (12%) |

Most repeated subjects come from changes that apply to every
repository: the voice rules, the agent instructions, the guides as
Agent Skills, Prometheus metrics, and the CI workflow. The project
expects more changes of that kind. In one repository, each one is a
single commit.

## One repository

The repository is `liken-sh/liken`. Its top level lists the things
that ship, and each one keeps its own domains inside it:

```
liken/                     the OS: init/, image/, kernel/, k3s/, machine-operator/, ...
kubernetes/                the client, the watch, and the cache that every operator imports
brand/                     the theme, voice.md, crdref, and linkcheck
vulkan/  vaapi/  ffmpeg/  mpv/  weston/
audio-operator/  bluetooth-operator/  display-operator/  equipment-operator/
media-operator/  library-operator/  people-operator/
git-csi-driver/  per-node-csi-driver/
liken.sh/                  the DNS zone, the release channel, and the organization's repositories
plans/
AGENTS.md  .agents/skills/  Makefile
```

- `liken/` holds today's tree. Inside it, the paths do not change:
  this plan's `kernel/`, `image/`, and `releases/` are
  `liken/kernel/`, `liken/image/`, and `liken/releases/`.
- `kubernetes/` leaves `liken/` because the operators import it too.
  It replaces the seven caches, and `bus/` or a similar directory
  replaces the copies of `mqtt.go` and `leader.go` once a second
  user needs them.
- `plans/` moves to the top, because the plans now cover every
  component.
- `brand/` comes in. The sites read it from the tree, so no
  submodule pin can lag.
- `.agents` comes in. Its `AGENTS.md` becomes the root `AGENTS.md`,
  and its skills join `liken`'s agent skills in `.agents/skills/`. A
  component's own `skills/` holds a different kind of skill: the ones
  its manual publishes for its users.
- `liken.sh/` moves to the top, because the domain, the release
  channel, and the settings of every repository cover the whole
  project. `.envrc`, which hands its Terraform the credentials, moves
  with it.
- The root `Makefile` runs targets across components, such as
  `make preview`, which serves every manual together.

Four repositories stay out:

- `corrosion` is a fork of superfly/corrosion, with its own upstream
  and its own branches.
- `plugins` stays the skill catalog.
- `log` is the devlog site.
- `liken-dev-cluster` stays its own repository.

### The history moves with the code

`liken`'s own tree moves into `liken/` with `git mv` in one commit.
Its history is not rewritten, so the commit SHAs that published
releases and the channel name stay valid.

Each other repository comes in with its full history. `git
filter-repo` rewrites the repository so that every path is under the
component's directory, and prefixes every tag with the component's
name. Then a merge with `--allow-unrelated-histories` joins it to
`main`:

```sh
git filter-repo --to-subdirectory-filter display-operator --tag-rename '':'display-operator/'
git merge --allow-unrelated-histories display-operator/main
```

The tag prefix is necessary, because every operator repository has
tags such as `2026.09.28-001`, and the names collide without it.
The imported commits get new SHAs. The old SHAs still resolve in the
archived repositories, so a development version that names an old
SHA still points at a real commit. On 2026-09-28 the imports total
about 1,280 commits: the nine operator and driver repositories,
`brand`, and `.agents`.

### One docs site

The project publishes one site, `liken.sh`. Each component's manual
is a section at `liken.sh/<component>/`, for example
`liken.sh/bluetooth/`, built from the component's own `docs/`
directory together with the rest of the site.

The names of the old sites stay in use. Each one is also a name that
a cluster shows: `bluetooth.liken.sh`, `display.liken.sh`, and the
other operator sites are API groups, and `git.liken.sh` and
`per-node.liken.sh` are CSI driver names. A person who sees one of
those names in a cluster and opens it in a browser must arrive at
that component's manual. A small host, a Flatcar Nanode that runs
Caddy (`liken.sh/redirects.tf`), answers each name with a 301 to
`liken.sh/<name>/`. A wildcard DNS record sends every `*.liken.sh`
name that has no record of its own to the host, and `www`,
`releases`, and `log` keep their own records. The host answers only
the names in a fixed list, `redirect_names`, because it gets one
certificate for each name, and a new component's name needs one
entry in that list. GitHub Pages cannot do this, because it serves
one custom domain for each repository and does not redirect for
another domain.

The old repositories are archived after the move, each with a
`README.md` that names its new directory. The project has no users
outside its maintainers yet, so no install path has to keep working
across the move.

`git-csi-driver`, `per-node-csi-driver`, and `people-operator` do not
need `liken` to run. They stay in the repository, and each one must
install on any Kubernetes cluster. A component moves out when a user
wants only that component and the repository is in the way. The CSI
driver names `git.liken.sh` and `per-node.liken.sh` can change
cheaply until a cluster outside the project holds volumes that name
them.

## A component

A component is a directory with a `package.toml`. CI finds every
component with `**/package.toml`, so a component can sit at the top
level, such as `mpv/`, or inside the OS, such as `liken/kernel/`. The
directory name is the component name, the image name, and the name
on the channel.

A pinned component declares its component in three files:

- `package.toml`: the upstream version, the `liken` revision, the
  license, the dependencies, the outputs, and each source file with
  its sha256 and its mirrors. The format follows stagex's
  `package.toml`, with two added tables, `[depends]` and
  `[outputs]`.
- `Dockerfile`: the build. It starts `FROM` a stagex image pinned
  by digest, adds the verified source files, and runs the build with
  `--network=none`. Its last stage is `FROM scratch` and holds only
  the files that ship.
- `README.md`: why `liken` needs the component, and the choices in
  its build. This text replaces the long header comment in each
  `fetch.sh` today.

An example for `mke2fs`:

```toml
[package]
name = "e2fsprogs"
version = "1.47.4"
revision = 1
license = "GPL-2.0-only"

[sources.e2fsprogs]
hash = "<sha256>"
file = "e2fsprogs-{version}.tar.gz"
mirrors = [
  "https://releases.liken.sh/components/e2fsprogs/{version}/source/",
  "https://mirrors.edge.kernel.org/pub/linux/kernel/people/tytso/e2fsprogs/v{version}/",
]

[outputs]
channel = true
```

```dockerfile
FROM ghcr.io/liken-sh/stagex/pallet-gcc-gnu-busybox@sha256:<digest> AS build
ADD e2fsprogs-1.47.4.tar.gz /
WORKDIR /e2fsprogs-1.47.4
RUN --network=none ./configure --disable-nls LDFLAGS=-static \
 && make -C misc mke2fs

FROM scratch
COPY --from=build /e2fsprogs-1.47.4/misc/mke2fs /sbin/mke2fs
```

A tracked component has no `version` and no `revision`. Its version
is the repository tag, and its source is its own directory:

```toml
[package]
name = "media-operator"
license = "Apache-2.0"

[depends]
components = ["kubernetes", "mpv", "vulkan"]

[outputs]
images = ["media-operator", "media-operator-api", "media-operator-player"]
deploy = "deploy/"
```

`[depends]` names other components. `[outputs]` names where the build
goes: `channel` for files on `releases.liken.sh`, `images` for
images on `ghcr.io/liken-sh`, and `deploy` for a directory of
manifests that the build pushes as an OCI artifact. A component can
have more than one kind of output.

One driver builds any component from these files. It reads the
graph, fetches and verifies the sources, runs the builds with
BuildKit, writes the build record, and publishes. The `licensing`
domain reads the same `package.toml` files for the notices and the
source offer, so the list of sources is written in one place only.

The `latest.sh` scripts stay one per domain. Each upstream announces
its releases in a different way, so each domain keeps its own script
to find the newest version.
The `bump-components` skill and `make versions` keep working on top
of them. A bump now also writes a new revision and, where the build
reproduces, the new expected digests.

## The base images are pinned components

`vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston` are upstream software
that the project builds from source into container images. Each one
becomes a pinned component with its own directory, its own upstream
version, and its own revision. Its output is an image, not files on
the channel.

The operators build `FROM` the base in the tree, not from a published
tag. `docker buildx bake` gives each consumer a named build context
that points at the base's target:

```hcl
target "mpv" {
  context = "mpv"
}

target "media-operator-player" {
  context  = "media-operator"
  dockerfile = "Dockerfile.player"
  contexts = { mpv = "target:mpv" }
}
```

`FROM mpv` in `Dockerfile.player` then resolves to the `mpv` target's
output at the same commit. A change to a base reaches every consumer
at the next build, with no pin to move by hand.

## When a component builds

### A pinned component builds when its revision is new

The `liken` revision is the declaration. The first build of an
upstream version is revision 1. A change that alters the output but
keeps the upstream version increments the revision, for example a
kernel config change or a new stagex image. The pin in the domain
changes from `7.2.6-1` to `7.2.6-2`, and that one line is the bump.

CI enforces the rule with a recipe hash. The hash covers
`package.toml`, the `Dockerfile`, every file in its build context, the
digest of each image it builds from, and the published revisions of
the components in `[depends]`. For each pinned component, CI compares
the hash with the one in the published build record for the pinned
revision: the `sh.liken.recipe` label of an image, or
`component.yaml` on the channel:

- If a published revision exists and its hash matches, CI builds
  nothing for the component. A consumer image builds on it again from
  the layer cache, and the OS build downloads a channel output.
- If a published revision exists and its hash differs, CI fails. The
  recipe changed without a bump.
- If no published revision exists, CI builds the component.

Because the hash covers the dependencies, a bump cascades. When
`ffmpeg` goes from revision 3 to 4, `mpv`'s hash changes, and CI fails
until `mpv` bumps too. The `bump-components` skill writes the cascade
in the same commit: it walks the graph from the bumped component and
increments the revision of each pinned dependent. The commit touches
several `package.toml` files, and each new revision is a line that a
person reviews.

When a component's build reproduces, the domain also commits the
expected sha256 of each output file, as stagex does in its `digests/`
directory. A bump then changes two lines: the version and the
expected digests. A rebuild by anyone must produce the same digests.
Components that do not reproduce yet skip this check, and their
`README.md` says so.

### A tracked component builds when its paths changed

A tracked component's paths are its own directory plus the
directories of every component it depends on, followed through the
whole graph. On a release tag, CI does this for each tracked
component:

1. Reads the component's newest published release version from
   ghcr, or from the channel for the OS.
2. Diffs the component's paths from the git tag of that version to
   the commit of the new tag.
3. Builds and publishes the component if the diff is not empty, or
   if no git tag exists for that version.

The git tag of a version is the bare version for a release made in
this repository, and `<component>/<version>` for a release that the
component made before the move. So the first release after the move
diffs from each component's imported tag, and it does not rebuild a
component that did not change.

The diff starts from the component's own newest published version,
not from the previous repository tag. If a release fails halfway,
the component that did not publish keeps its older version, and the
next release diffs from there and builds it. The rule for a missing
git tag covers a new component and the first release after the move,
when the old tags are in the archived repositories.

A tracked component that did not change gets no new version. Its
cluster keeps the version it has, so its pods do not restart.

A push, a pull request, and a dispatch run only what reads the
change:

- A dependency reaches a component only through the files that the
  dependency's outputs are built from. Its manual, plans, notes, smoke
  checks, Go tests, and `testdata/` reach no dependent.
- The generated files hold one part for each component. A change to a
  component's workflow, to its calls in `ci.yaml`, or to its targets
  in `docker-bake.hcl` runs that component, and no other.
- Inside a component, each job runs on any change to the component or
  to an output of its closure, because a test can read any file of its
  component. An image runs only when what BuildKit sends it changed:
  its contexts less their ignore files, its Dockerfile, its bake
  target, its smoke check, or an image it builds on. A change to
  `brand/` alone runs the hugo jobs of the manuals.
- A run that publishes a component runs all of its jobs and images
  first.
- A component's checks wait only for the pinned bases its images build
  on. Its publish waits for the checks of its whole closure.
- A change to a component's publish job, to its publish call, or to
  `ci/publish.go` runs the publish job in a dry mode in the check
  stage: it builds what a publish would push, and pushes nothing.

The dependencies are declared by hand in `[depends]`, and a missing
entry fails silently: the component does not rebuild, and it keeps
running an old base. CI checks the declaration against what the
tools report: `go list -deps` for Go imports inside the repository,
`cargo metadata` for path crates, and the `FROM` lines and the bake
contexts for images. A dependency that the tools find and
`[depends]` omits fails the build.

## Versions and tags

The repository has one tag format, CalVer `yyyy.mm.dd-nnn`, and it
applies to the whole repository. `nnn` is a three-digit serial within
the day, starting at `001`. The tag is the bare version, with no `v`
prefix, and it is lightweight. The tag is the release act. Everything
after it is verification.

A tag releases every tracked component that changed, each under the
tag's version, and every pinned component whose revision is not
published yet, each under its own version. After a release on
2026-10-02, the registry could show:

```
display-operator   2026.10.02-001   changed
media-operator     2026.10.02-001   changed, because mpv changed
mpv                0.41.0-2         pinned, new revision
audio-operator     2026.09.20-003   unchanged, keeps its version
```

CI also publishes a release record for each tag: every component and
its version at that tag. At release X, a tracked component's version
is its newest published version at or before X.

A push to `main` is a development build. It builds each tracked
component whose outputs changed since its newest published version,
and each component that depends on one. A development version names
its commit, so the diff starts there, and a build that failed to
publish is published by the next push. The checks of a push to main
also diff with the newest main run that passed, and every job runs
when the plan cannot find that run. The version comes from `git describe` of the newest
repository tag, with a suffix: `2026.10.02-001-dev-017-abcdef01` is
17 commits past `2026.10.02-001`, at commit `abcdef01`. The count is
for the whole repository, not for the component, so it is not
contiguous for one component. It sorts after its release and before
the next one, and that is its only job. A development build never
moves `:latest`.

A lab release of the OS keeps its `-9xx` serial, and serial `000`
stays the working-tree channel, as the `release` skill describes.

## The OS is a tracked component

`liken/` is a tracked component. Its dependencies are the pinned
components it pulls into the image, `kubernetes/`, and its own
domains, such as `init/`, `machine-operator/`, and `image/`. Its
output is a release on the channel, with the smoke drills and the
publish that the `release` skill describes.

A tag releases the OS when any of those inputs changed since the last
OS release. So a tag that is meant to release one operator also
releases the OS if an OS change is waiting on `main`. An OS release
reboots every machine in a fleet that follows releases. The project
accepts this. Over time the OS changes the least, and the operators
change the most, so most tags do not include an OS release. When an
operator really needs a new OS, the same rule ships both together.
The goal is that no operator needs a specific OS release, but it is
not a rule that CI enforces.

## Where each output goes

| Output | Host | Tag or path |
| --- | --- | --- |
| Files of a pinned OS component | `releases.liken.sh/components/<name>/<version>/<revision>/` | the revision |
| The OS release | `releases.liken.sh/<version>/` | the repository tag |
| An image | `ghcr.io/liken-sh/<image>` | the component's version |
| A `deploy/` directory | `ghcr.io/liken-sh/<component>-deploy` | the component's version |

A publish uploads the outputs first and the record that makes them
visible last:

- On the channel, `component.yaml` goes last, so a component with no
  `component.yaml` does not exist yet.
- On ghcr, the deploy artifact goes last, after every image of the
  component. A cluster follows the deploy artifact, so it never
  applies manifests that name an image that is not pushed yet.

CI never overwrites a published revision or a published version.

## How a cluster follows a component

A cluster follows each component through one `OCIRepository` and one
`ImagePolicy`. Flux's `OCIRepository` is GA at
`source.toolkit.fluxcd.io/v1` in Flux 2.9. It pulls a tarball of
manifests from a registry by tag, semver range, or digest, and a
`Kustomization` applies it the same way it applies a git checkout.

```yaml
apiVersion: image.toolkit.fluxcd.io/v1
kind: ImageRepository
metadata:
  name: display-operator
  namespace: flux-system
spec:
  image: ghcr.io/liken-sh/display-operator-deploy
  interval: 1h
---
apiVersion: image.toolkit.fluxcd.io/v1
kind: ImagePolicy
metadata:
  name: display-operator
  namespace: flux-system
spec:
  imageRepositoryRef:
    name: display-operator
  filterTags:
    pattern: '^(?P<v>\d{4}\.\d{2}\.\d{2}-\d{3})$'
    extract: "$v"
  policy:
    alphabetical:
      order: asc
---
apiVersion: source.toolkit.fluxcd.io/v1
kind: OCIRepository
metadata:
  name: display-operator
spec:
  interval: 1h
  url: oci://ghcr.io/liken-sh/display-operator-deploy
  ref:
    tag: 2026.10.02-001 # {"$imagepolicy": "flux-system:display-operator:tag"}
```

The same marker also sets the tag of each image in the
`Kustomization`, so the manifests and the images move in one commit.
The policy watches the deploy artifact, not an image, because the
deploy artifact is published last.

The pattern chooses the track:

- A cluster that follows releases uses the pattern above.
- A test cluster uses a pattern that admits development builds:
  `'^(?P<v>\d{4}\.\d{2}\.\d{2}-\d{3}(-dev-\d{3}-[0-9a-f]{8})?)$'`.
- A cluster that follows releases can follow one component's
  development builds, to test hardware that only that cluster has.
  The change is that one component's pattern, in one commit.

A development build pushes its deploy artifact under its development
version, so no cluster pins a commit sha.

## The order of the work

The work has two stages. This plan holds stage 1, and
[plan 72](../72-the-os-builds-from-source.md) holds stage 2. Each step
ends with CI green and a release rolled to liken-1. A step that
changes the OS also ends with the smoke drills green.

### Stage 1: one repository

This stage moves the repositories together and builds the graph. It
changes how every component releases, and it removes the copies that
the numbers in "The split repositories" measure. It does not change
how any vendored domain gets its bytes.

1. **The move.** Move `liken`'s tree into `liken/`, and bring in the
   operators, the drivers, `brand`, and `.agents`, on a branch. Each
   component keeps its own `Makefile` targets. GitHub runs workflows
   only from `.github/workflows/` at the repository root, so the
   imported workflow files stop running. One root workflow replaces
   them for this step. It has one job for each component, a path
   filter on each job, and `needs:` from each consumer to the base
   images it builds on. It runs each component's `make` targets and
   publishes nothing. The branch merges when that workflow is green
   for every component.

   Built 2026-09-28. Every component moved at once, on one branch,
   and the old repositories were archived after the merge. The root
   workflow has 57 jobs and no `needs:` between components yet,
   because the base images are still stages in `display-operator`'s
   Dockerfile until step 5. `brand/` replaced its eleven submodules in
   the same step: the sites set `themesDir`, the Go modules take a
   `replace`, and the Rust image builds take `brand/` as a second
   build context. The operator sites still serve from the archived
   repositories' Pages until the one site replaces them.
2. **The graph and the tag.** Add a `package.toml` to each tracked
   component. Build the driver's graph, the path diff, the release
   record, and the development builds. Replace the hand-written root
   workflow with one that a generator writes from the `package.toml`
   files, and commit the generated file. CI fails if the committed
   workflow differs from what the generator writes.

   The publish job has a dry-run mode, and it runs in that mode on
   every push to the branch. It prints each component, whether it
   changed and why, the version it would get, and each image and
   artifact it would push. It also checks that the workflow can
   write to each `ghcr.io/liken-sh` package. Each package is linked
   to its old repository, and it accepts a push from this
   repository's workflows only after its settings grant this
   repository write access. The first real publish comes after the
   dry run is correct and every package grants access.

   Built 2026-09-28. `ci/` is a Go program that reads every
   `package.toml` and writes `.github/workflows/ci.yaml` and one
   reusable workflow for each component,
   `component-<name>.yaml`. A component's call `needs:` the
   components in its `[depends]`, so the run page draws the graph,
   and each component's jobs sit in its own box. The `graph` job
   fails when the committed workflows differ from what `ci` writes,
   and when a `go.mod`, a Cargo workspace, a `FROM` line, or a build
   context uses a component that `[depends]` omits. `package.toml`
   gained three tables that this plan did not show: `[docs]`, which
   gives the manual's prefix in the one site, `[[jobs]]`, which names
   each check and the setup it needs, and `[[outputs.images]]`, which
   gives each image's Dockerfile, target, platforms, build contexts,
   aliases, and smoke check. The loader refuses a key it does not
   know, so a misspelled `version` fails instead of reading as a
   tracked component.

   The release record is the GitHub release for the tag. The deploy
   artifact goes out with this step, not with step 3: its
   `kustomization.yaml` names the component's images at the
   artifact's own version, so a cluster pins only the
   `OCIRepository`'s tag. The path diff leaves out `docs/`, `plans/`,
   `AGENTS.md`, `README.md`, and `smoke/` at a component's top, and
   every Go test file and `testdata/` directory. A change to them runs
   the component's own jobs, and no dependent's. The OS takes
   `brand` into its channel index pages, so a change to `brand`
   releases the OS.

   A real publish needs the repository variable `PUBLISH`. The write
   check starts a blob upload on each package and cancels it, so it
   leaves nothing behind.
3. **The deploy artifacts and the fleets.** Push each component's
   `deploy/` as an OCI artifact. The `liken` side is finished and
   published before any fleet moves. Move liken-1 to
   `OCIRepository` and the new policies first, as the proof. Then
   move each fleet that follows releases.

   A fleet that follows releases through the old `GitRepository`
   pins must suspend flux for the `liken` components before the
   first real publish from this repository. The suspension covers
   the `Kustomization`s and the image automation. The new releases
   push new tags to the same images, and the image automation would
   write each new tag into a `GitRepository`'s `ref.tag`. The old
   repositories never get those git tags, so the `Kustomization`
   would fail when it resumes. The running pods do not change while
   flux is suspended. The fleet resumes each component when its pins
   move to the new `OCIRepository`.

   liken-1 moved on 2026-09-29, at release `2026.09.29-001`, the
   first release from this repository. liken-1 declares no flux
   feature, because that feature syncs from a git repository and the
   testbed must not read the fleet's. So liken-1 runs only Flux's
   source and kustomize controllers, installed by hand, with no git
   source. Each component has one `OCIRepository` and one
   `Kustomization` beside the cluster's own policy objects, and a
   bump is an edit of one tag and an apply. The `Kustomization`s set
   `deletionPolicy: Orphan`, so a deleted `Kustomization` leaves the
   CustomResourceDefinitions and every object of their kinds in
   place. A server-side dry run against the live objects showed only
   the new images and new labels before the move. The OS moved to the
   same release through the Cluster's catalog, and both machines took
   their turns and came back Ready.

   The move found one fault in the deploy artifacts. Five bases set
   the top-level `namespace` field, and Flux merges a
   `Kustomization`'s components into the artifact's own
   `kustomization.yaml`, so the field moved each monitoring
   component's dashboard into `liken-system`. Those bases now use the
   `NamespaceTransformer` with `unsetOnly`, as the others did.

   The fleets that follow releases moved on 2026-09-29, after
   liken-1. Each follows every component through one `OCIRepository`
   and one `ImagePolicy`, and image automation writes each new release
   tag into the `OCIRepository`. The OS moved to `2026.09.29-002`
   through the Cluster's catalog, and its monitoring component follows
   the release tag at `liken/monitoring`.
4. **The shared module.** Replace the copies of the client, the
   watch, and the cache with `kubernetes/`, one operator at a time.

   Built 2026-09-29. The six operators, and `liken`'s machine
   operator, cluster operator, and CLI, import `kubernetes/`. `liken`
   keeps its wake handlers and one rule for reads in
   `liken/kubernetes/watch`: a ready store that does not hold an
   object, and whose memo has not noted it, answers that the object
   does not exist. For `liken`, the module gained an in-cluster address
   and request timeout, a write guard, the address of the watches, a
   store's indexes, a memo that drops a record once the store holds its
   version, an option that stops a store after any failed watch, and an
   alias for the memo's metadata type, so that `init` still links no
   `net/http`. liken's operators set that option on every watch: after
   a k3s restart, a store can miss the writes made while its reflector
   waits out a backoff, and a machine must not stage a rollout or
   reboot from it.

5. **The base images.** Make `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and
   `weston` pinned components, and make their consumers build
   `FROM` the tree.

   Built 2026-09-28. Each base has its own directory at the top, with
   a `package.toml`, a `Dockerfile`, a `README.md`, and its closure
   script, and `display-operator` is a consumer like the others. The
   bases are not source builds yet. Each one installs Debian packages
   and ships their library closure on `scratch`, as the stages in
   `display-operator`'s Dockerfile did. So a base's version is not an
   upstream version: it is the date of the snapshot.debian.org archive
   that the build installs from, as `YYYYMMDD`, and the tag is
   `<version>-<revision>`, such as `20260928-1`. Every base starts
   `FROM debian:trixie-slim` by digest, installs from that one
   snapshot, and upgrades the image's own packages to it. The five
   bases share one date and one digest, so a library that two of them
   hold is the same file. The sources name `http`: the slim image has
   no CA certificates, and apt checks every package against the signed
   Release files.

   The build file is named `Dockerfile` everywhere in this plan now,
   the name Docker reads by default. The build record of a base is the
   label `sh.liken.recipe` on the published image. The recipe covers
   the files that Docker sends in the build context, so a base's
   `.dockerignore` keeps its `README.md` and its `smoke/` check out of
   it; a `<Dockerfile>.dockerignore` takes precedence, as it does in
   BuildKit. It covers only the executable bit of each file's mode, as
   git does, so the umask of a checkout does not change it. It covers
   each named directory context that lies outside the build context of
   every image in the dependencies, each image outside the repository
   that a `FROM`, `COPY --from`, or `RUN --mount` names, each file that
   an `ADD` fetches, and the settings of the image's bake target. A
   format number in `ci/recipe.go` changes every recipe when the way
   the generator builds an image changes. The recipe refuses an image
   outside the repository with no digest, an `ADD` from the network
   with no `--checksum`, and an image of the repository whose
   component is not in the dependencies. A pinned component depends
   only on pinned components, publishes images only, and never moves
   `:latest`. When its tag is published and its hash matches, CI
   publishes nothing for it, and each consumer's bake build makes the
   base again from its layer cache. Its jobs still run when its own
   paths change, so a new smoke check runs.

   The snapshot sources use `http`, and `Check-Valid-Until` is off for
   an old snapshot, so a host on the path could serve an older
   `InRelease` that Debian also signed. `vulkan/snapshot.sha256` holds
   the sha256 of each `InRelease` file of the date, and `snapshot.sh`
   checks them after `apt-get update`. The file is in the `vulkan`
   recipe, so the date and the package lists are bound together.

   Off main, a pinned base's images job also writes its layers to
   ghcr as the tag `:buildcache-<version>-<revision>`, and every build
   reads that tag beside `:buildcache`. A consumer's job runs after
   its bases' jobs, so on a branch that raises a base's revision the
   consumer builds the base from the layers that the base's job wrote,
   not from snapshot.debian.org again. The tag names the revision, so
   two branches that build different revisions do not overwrite each
   other, and there is one such tag for each revision that a branch
   built. The GitHub Actions cache is not used: it holds 10 GB for the
   whole repository, and the Go, Rust, and OS input caches fill it.

   `ci generate` also writes `docker-bake.hcl`, with one target for
   each image. The generator reads each Dockerfile's `FROM` and
   `COPY --from` lines for the stages that the image's target reaches,
   and it gives each base that they name a `target:` context. The
   publish job builds through the same file. `ci deps` counts a bare
   `FROM mpv` as a use of `mpv`.

   The shared stages went to the lowest base that uses them.
   `closure.sh` and `snapshot.sh` are in `vulkan/`, and the other
   bases take that directory as the build context `builder`. The
   hotplug shim and the layout module are in `weston/`, because only
   the compositor loads them, and they must link the glibc of the
   compositor's snapshot. A change to either one is a new `weston`
   revision.

## Failures and what the design does about them

| Failure | What happens |
| --- | --- |
| A fork pull request bumps a component | The driver builds it in the run. Nothing is published. |
| A pinned dependency bumps and its dependents do not | The dependents' recipe hashes differ from their published revisions, and CI fails until they bump. |
| `[depends]` omits a dependency | The check against `go list`, `cargo metadata`, and the `FROM` lines fails the build. |
| A release fails halfway | Each component that did not publish keeps its older version. The next tag diffs from that version and builds it. |
| The first release after the move | Each component diffs from its imported `<component>/<version>` tag. A component with no tag for its newest version builds. |
| A fleet on the old `GitRepository` pins sees the first release from this repository | Its image automation writes a git tag that the old repository does not have. Suspending the image automation and the `Kustomization`s before the first real publish prevents it. |
| The first real publish cannot push to a `ghcr.io/liken-sh` package | The dry run checks write access to every package before the first real publish. |
| A cluster sees a new tag before the images exist | It cannot. The deploy artifact is published after the images, and the policy follows the deploy artifact. |
| A tag meant for one operator also carries an OS change | The OS releases too. The project accepts this; see "The OS is a tracked component". |

## Decisions

- **The repository first, stagex second.** The numbers in "The split
  repositories" are a cost that the project pays every week. Stage 2
  pays for itself through Secure Boot and the claim, and it can wait
  until those are the priority. Stage 2 also uses the driver and the
  graph that stage 1 builds.
- **One repository, not twelve and not four.** The shared code
  crosses every line that a smaller split could draw: the cache is
  in the OS and in every operator, and the base images feed display,
  media, and library. Four domain repositories would still need a
  published module and pins between them. A smaller fix was also
  possible: one shared Go module in its own repository, reusable
  GitHub workflows, and a script that tags several repositories.
  That fix removes the copies of the plumbing. It does not make a
  change that applies to every repository into one commit, it does
  not remove the base-image pins, and a session still pushes up to
  ten tags.
- **A generated static workflow, not a dynamic matrix.** A job for
  each component, with `needs:` that follow the graph, makes GitHub
  draw the dependency graph on every run page, and a skipped job
  shows which component did not change. A matrix that a script
  computes shows a flat list and keeps the graph inside the script.
  A generator writes the static file from `package.toml`, so the
  graph has one source.
- **Freeze release tags during the move, and keep CI.** The old
  repositories keep their workflows until each one is archived, so
  an urgent fix can still release from the old repository. No
  release tag is pushed in any repository from the start of step 1
  until the branch merges.
- **The full history, with prefixed tags.** `git log` and `git
  blame` inside a component directory show the component's whole
  history, and the old tags give the first release a place to diff
  from. Only `liken`'s history keeps its SHAs, because published
  releases name them.
- **One site, and the old names as redirects.** A separate site for
  each component split the manual by repository, not by what a
  reader looks for. The redirect keeps the one property of the old
  sites that matters: the name in the cluster opens the manual.
- **The OS directory is `liken/`.** The directory name is the
  component name, and the OS component and its artifacts are named
  `liken`. The Go import paths become
  `github.com/liken-sh/liken/liken/...`.
- **Two ways to version, one model.** A pinned component's version
  comes from upstream, and its revision is a declaration that a
  person reviews. A tracked component has no upstream, so the
  repository tag is its version. The directory, the file, the graph,
  the publish steps, and the attestations are the same for both.
- **One tag format for the whole repository, not one per
  component.** A tag such as `display-operator/2026.10.02-001` would
  let each component release alone. The path diff gives the same
  result: a component that did not change gets no new version, and
  its pods do not restart. So the person who tags decides when to
  release, and the graph decides what.
- **A path diff for tracked components, not an input hash.** The
  paths come from the graph, which changes slowly. The diff starts
  from each component's own newest published version, so a failed
  release heals on the next tag.
- **A recipe hash for pinned components, and a cascade by tooling.**
  The hash includes the published revisions of the dependencies, so
  a dependency's bump cannot reach a dependent without a reviewed
  revision. The `bump-components` skill writes the cascade.
- **Consumers build `FROM` the tree.** A change to a base reaches its
  consumers at the next build. Hand-moved pins drifted between
  consumers, and the tree cannot drift.
- **Manifests ship as OCI artifacts, not as git refs.** A cluster
  reads one version from one registry for both the manifests and the
  images. The git tag format then stays internal to the repository,
  and a development build needs no commit sha.
- **The OS releases when its inputs changed, with the rest.** The OS
  is a tracked component like the others, and the project accepts
  that a tag can release it.

## Open questions

- **Where the redirect service runs.** It needs a host that answers
  `*.liken.sh` over HTTPS with a wildcard certificate. Linode DNS
  has no redirect feature, and a Linode community answer says that
  Object Storage does not serve redirects.

  Decided on 2026-09-28: a Linode Nanode that runs Flatcar Container
  Linux, declared in `liken.sh/redirects.tf` beside the zone. A
  Nanode costs $5 a month, and Flatcar is immutable and updates
  itself. The host runs one container, the official Caddy image, and
  the Terraform plan renders its whole configuration into one
  Ignition config, so a change makes a new instance and nobody
  changes the host by hand. The small Go service that answered the
  redirects is removed, because a Caddy `redir` does the same work.
  Caddy gets one certificate for each name over HTTP-01 or
  TLS-ALPN-01, in place of the wildcard certificate, because a
  wildcard certificate needs DNS-01, and DNS-01 puts a token that can
  edit the whole zone on an internet-facing host. The names are a
  fixed list, `redirect_names`, in place of any name that arrives. A policy that
  asked for a certificate for any name would let a stranger spend the
  weekly Let's Encrypt quota of liken.sh with random names, and
  GitHub Pages renews the apex certificate from that quota. The DNS
  wildcard still sends every name without a record of its own to the
  host, so a new component's name needs one entry in the list, not a
  new record. A name redirects only after its CNAME to GitHub Pages
  leaves `terraform.tf`, because a record of its own wins over the
  wildcard.

  The first boot is Flatcar `4459.2.4`, not the newest stable
  release. Linode refuses an uploaded image that is larger than 6144
  MiB: it refused `4757.2.0`, whose image is 8,455,716,864 bytes
  uncompressed, with "8064 MB exceeds single-image storage limit of
  6144 MB". `4459.2.4` is the newest stable release with Flatcar's
  first disk layout, and its image is 4,756,340,736 bytes. The host
  keeps its automatic updates, so it moves to the current stable
  release soon after its first boot. An update does not change the
  partitions, and Flatcar plans to keep updating the first layout
  until at least 2030 (flatcar/Flatcar#1917).

## Sources

Checked on 2026-09-25:

- stagex: <https://stagex.tools/>, the package list at
  <https://stagex.tools/packages/>, the documentation at
  <https://docs.stagex.tools/overview/>, and the repository at
  <https://codeberg.org/stagex/stagex> (the `e2fsprogs`, `iptables`,
  and `grub` recipes under `packages/user/`, and the `Makefile`'s
  `verify` and `digests` targets).

Checked on 2026-09-28:

- Flux `OCIRepository`, `source.toolkit.fluxcd.io/v1`, with
  `ref.tag`, `ref.semver`, and `ref.digest`, and cosign and Notation
  verification: <https://fluxcd.io/flux/components/source/ocirepositories/>
  (the page for Flux 2.9).
- Flux image automation markers, which are inline comments in the
  target YAML: <https://fluxcd.io/flux/guides/image-update/>.
- Docker Bake, a target as a named build context with
  `contexts = { name = "target:<target>" }`:
  <https://docs.docker.com/build/bake/contexts/>.
- `git filter-repo`, `--to-subdirectory-filter` and `--tag-rename`:
  <https://github.com/newren/git-filter-repo>.
- A Linode community answer that Object Storage does not serve
  redirects, with no date on the answer:
  <https://www.linode.com/community/questions/19668/how-can-i-add-a-redirection-rule-to-my-bucket>.
- Flatcar on Linode, an uploaded image and an Ignition config in the
  instance's metadata user data:
  <https://www.flatcar.org/docs/latest/deploy/cloud/akamai/>.
- The Linode regions API, which lists `Metadata` among the
  capabilities of `us-east`: <https://api.linode.com/v4/regions/us-east>.
- Caddy's `redir` directive and its placeholders. `{labels.2}` is the
  third label of the host from the right, and `{uri}` is the request
  URI with its query: <https://caddyserver.com/docs/caddyfile/directives/redir>,
  <https://caddyserver.com/docs/caddyfile/concepts#placeholders>, and
  `modules/caddyhttp/replacer.go` at `v2.11.4` in
  <https://github.com/caddyserver/caddy>.
- Let's Encrypt's rate limits: <https://letsencrypt.org/docs/rate-limits/>.
- Linode's limits on an uploaded image:
  <https://techdocs.akamai.com/cloud-computing/docs/upload-an-image>.
- Flatcar's plan to keep updating the first disk layout until at
  least 2030: <https://github.com/flatcar/Flatcar/issues/1917>, and the
  change to the larger layout:
  <https://github.com/flatcar/scripts/pull/3027>.
- Caddy's HTTP server, which answers an ACME HTTP-01 challenge before
  it runs any site's routes: `modules/caddyhttp/server.go` at
  `v2.11.4`. certmagic's retries of a failed certificate, up to six
  hours apart and for at most 30 days: `async.go` at `v0.25.3` in
  <https://github.com/caddyserver/certmagic>.
- GitHub Pages custom domains, one for each repository:
  <https://docs.github.com/en/pages/configuring-a-custom-domain-for-your-github-pages-site/about-custom-domains-and-github-pages>.
