# Weston and mpv

The `weston` entry of `weston/package.toml` and the `mpv` entry of
`mpv/package.toml` are exceptions to the snapshot bump in section 5
of `SKILL.md`. Those images build Weston and mpv from the upstream
releases, not from packages of the snapshot.

- `weston/latest.sh` and `mpv/latest.sh` report and bump those
  entries. The URL and digest of the tarball are in the Dockerfile,
  and in the `pin.toml` beside it, which the image's SBOM record
  reads.
- The snapshot has no package by that name that either image
  installs. `built_from_source` in `vulkan/latest.sh` lists both
  entries, so the report and the `--bump` of the snapshot skip them.
- For a bump of mpv, follow "Bumping mpv" in `mpv/README.md`. It
  covers the two cases in which `mpv/latest.sh --bump` stops: a patch
  in `mpv/patches/`, and a Debian snapshot whose libplacebo is older
  than the release needs. It also covers the new meson options and
  the revision.
