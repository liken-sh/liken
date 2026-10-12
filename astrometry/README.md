# astrometry

`ghcr.io/liken-sh/astrometry` holds the programs that plate-solve a
frame: `astrometry-engine` and `solve-field` from astrometry.net, and
`source-extractor` from SourceExtractor, with every library they load,
on `scratch`. `astrophotography-operator`'s worker image builds on it.
The worker extracts the stars of a frame beside the frames, and the
solver solves the star list against the index files, as
[`astrophotography-operator` plan
02](../astrophotography-operator/plans/02-solving-and-centering.md)
describes.

## What the image holds

`astrometry-closure.sh` names the three programs as seeds, collects
every library the loader resolves for them, and adds SourceExtractor's
filters and its neural network from `/usr/share/source-extractor`. The
image holds no Python and no index files. `solve-field` runs Python
only for `--just-augment` without `--no-remove-lines` and
`--uniformize 0`, so a caller passes both. The caller passes
`astrometry-engine` a configuration that names the index files.

The image holds the copyright file of each Debian package it takes
files from, and the list `/usr/share/doc/liken/astrometry.packages`,
as [`notices/`](../notices/) describes. astrometry.net is
BSD-3-Clause with GPL parts, and SourceExtractor is GPL-3.0+.

`smoke/astrometry.sh` runs the image before it ships. It extracts the
stars of a synthetic frame, builds an `.axy` with the options above,
and solves it against a small index file of the same synthetic stars.
`smoke/field.py` says how the frame and the index file were made.

## The version and the revision

`astrometry` is a pinned component. Its `package.toml` states a
version and a revision, and the image publishes under the tag
`<version>-<revision>`, for example `20260928-1`.

- The **version** is the date of the Debian snapshot that the build
  installs from, as `YYYYMMDD`. It moves with the bases on Debian,
  because `vulkan/snapshot.sha256` holds the checksums of one date.
- The **revision** counts the changes to the recipe at one version. It
  starts at 1 for a new version. Any change to the build that keeps
  the version needs the next revision: a change to the `Dockerfile`, to
  a script, to the Debian image digest, or to `package.toml`.

The build reads `snapshot.sh` and `closure.sh` from the `vulkan`
directory, and `[depends]` names `vulkan`, so a new revision of
`vulkan` needs a new revision here too. The `bump-components` skill
under `.agents/skills` holds the procedure.

## Build

The bake file at the top of the repository builds this image, from
the top of the repository:

    docker buildx bake astrometry --load
