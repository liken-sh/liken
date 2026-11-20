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
- A bump of mpv can bring a new meson option, which the build leaves
  off until `mpv/Dockerfile` names it. So compare the release's
  `meson.options` with the list in the Dockerfile.
- `mpv/latest.sh --bump` stops with an error while `mpv/patches/`
  contains a patch. The new release can include the commit of the
  patch, and the patch then fails to apply. Delete each patch whose
  commit the new release includes, together with its URL in the
  `backports` of `mpv/pin.toml`, as `mpv/patches/README.md` says.
  Then run the bump again.
