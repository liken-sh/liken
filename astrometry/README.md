# astrometry

The `astrometry` image plate-solves a frame of the sky. It finds the
stars in the frame, and then the position on the sky that those stars
match, so a telescope can center a target. It contains the solver of
[astrometry.net](https://astrometry.net/) and the star extractor
SourceExtractor, from Debian's packages.

This component is a pinned image on `ghcr.io/liken-sh`, not an
operator. It contains the programs and every library that they load.
It contains no index files: the caller puts the index files in a
volume, and passes the solver a configuration that names them. The
image has no Python either. So a caller of `solve-field` must pass
the options that stop it from running Python. `astrometry-closure.sh`
lists those options.

`astrophotography-operator`'s worker image builds on this image. Its
workers mount the volume that contains the frames, and extract the
stars of each frame there. Its solvers solve the star lists against
the index files of a `Solver`. The image builds from the same Debian
snapshot as the base images, and its build scripts come from
`vulkan/`. So when the base images move to a new snapshot, this image
moves with them.

## The image

- `astrometry-engine` and `solve-field` from astrometry.net.
- `source-extractor`, with its convolution filters and its neural
  network.
- The copyright file and the record of each Debian package that the
  image copied files from, as [`notices/`](../notices/) describes. astrometry.net is
  BSD-3-Clause with GPL parts, and SourceExtractor is GPL-3.0+.

Before the image ships, `smoke/astrometry.sh` extracts the stars of a
synthetic frame and solves them against a small index file of the
same stars. `smoke/field.py` describes how the frame and the index
file were made.

MIT. See [LICENSE](LICENSE).
