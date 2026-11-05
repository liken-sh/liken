# 89, A release builds on the published bases

Proposed and built on 2026-11-05. `make bases` ran on a workstation
on 2026-11-05: all seven pinned bases passed their checks, and a build
of `library-operator-ffmpeg` pulled `ffmpeg` and `trust` by digest
and sent no request to snapshot.debian.org. The first CI run of the
change and the first release on it have not run yet.

## The problem

Every consumer of a pinned base built the base again from the tree, as
a named context of the bake file. A base installs its packages from
snapshot.debian.org, and the INDI images install from snapshots of
Ubuntu and of two PPAs. So each release of a consumer depended on
those servers, even when no base had changed for days.

The release `2026.11.04-001` showed the cost. Its tag released the OS
and three operators, and `library-operator`'s publish did not finish
in six attempts from 2026-11-04 to 2026-11-05, while snapshot.debian.org
answered `503` and `504`. The job built `ffmpeg` as a context of
`library-operator-ffmpeg`. In two attempts the build fetched about one
package every 15 seconds until the 30-minute job limit ended it, and in
another it failed on a `504`. The base's own tag, `ffmpeg:20261101-1`,
was on ghcr.io before the release, built from the same recipe.

The layer cache did not prevent it. `ffmpeg:buildcache` held 18
layers, and the job still missed the layer of `apt-get install ffmpeg`
and fetched every package again. A cold builder on a workstation
missed even `vulkan`'s layers. A cache hit depends on cache keys that
can differ between builders, so a cache can make a build faster, and
it cannot remove a server from a build.

## The rule

A consumer builds on a pinned base's published image when that image
is the build of the tree's recipe, and builds the base from the tree
otherwise. Three checks decide, and each reads the image by its
digest, so all three are about the same bytes:

1. The base's tag in the tree resolves to a digest on ghcr.io.
2. A signature of the base's own workflow, `component-<base>.yaml` at
   `main` or at a release tag, covers that digest. So CI built and
   pushed those bytes.
3. The `sh.liken.recipe` label, read through that digest, equals the
   recipe of the tree. The label is in the image's config, which the
   signed digest covers.

The recipe in `ci/recipe.go` covers every input of a pinned build: an
outside image by its digest, a download by its checksum, each file of
each build context, the bake settings, each dependency's tag, and,
through `snapshot.sh`, the checksum of each snapshot's `InRelease`
file. So two builds with one recipe had the same inputs, and the
published image is what a build from the tree would make. The one
input that the recipe does not cover is the BuildKit version.

The recipe of a consumer still names a base by its target, so a base's
publish changes no recipe above it.

## One command, on a workstation and in CI

`ci bases`, which `make bases` runs, runs the checks and writes
`docker-bake.override.hcl`: one variable for each base that passed,
holding its reference and digest, and a comment for each base that
did not, with the reason. `docker-bake.hcl` gives each pinned base
context a variable, `PUBLISHED_<BASE>`, that is empty by default, and
the context reads `target:<base>` while it is empty. bake reads the
override file after the generated file, and git ignores it.

CI's plan job runs the same command once for each run. Its output is
the file, and each images job and publish job writes it beside
`docker-bake.hcl` and names both files. So every build of a run uses
the same bases, and the plan's summary lists each one. A check that
fails or cannot run leaves its base to build from the tree, so a
registry or Rekor outage slows a build and never fails it.

## A bump is one commit

A commit that raises a base's revision names a tag that is not
published yet, so its run builds the new base from the tree and builds
each consumer on it, which tests the consumers on the new base at that
commit. The base's publish job pushes the tag, and every later run
pulls it.

## A published pinned image never builds again

A change to a workflow template or to the shared part of the bake file
runs every image, and before this plan that included each pinned
base, which reached the upstream servers again. Now a pinned image
whose tag is published with the tree's recipe never builds in a
check. The images job pulls the published image and runs its smoke
check, and an image with no smoke check runs nothing. The dry run of
its publish builds nothing either, because the publish of a published
tag only signs it again. The build code still runs in each tracked
component's publish and dry run, and in the first publish of each new
pinned tag.

## Choices set aside

- **The digest in git.** A second commit after each base's publish
  would record the digest, as `toolchains/images.toml` pins
  `corrosion`. It keeps every build's bytes in a commit, and it costs
  a second commit for each bump, with runs that still reach
  snapshot.debian.org until that commit lands. The maintainer chose
  one commit, with the checks at build time and the digest in each
  run's summary and provenance.
- **A mirror of the snapshot's packages.** It removes the dependency
  on snapshot.debian.org too, and it adds storage and a second pin for
  each package. The published base already holds the packages.
- **A larger layer cache.** The cache missed with every layer present,
  for the reason above.

## What to watch

The recipe now decides which bytes a consumer gets, so an input that
the recipe misses would no longer reach the consumers until the next
revision. `recipeFormat` in `ci/recipe.go` is the guard when the bake
template changes what a build makes. A cleanup of old package
versions on ghcr.io must keep each pinned base's published tag.
