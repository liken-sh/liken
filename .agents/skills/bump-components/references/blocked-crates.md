# A crate or a module that another one blocks

Another crate in the graph can block a crate whose newest release is
a new major version. `toolchains/latest.sh --bump crate:` then fails
with `failed to select a version for the requirement`, and the
message names the crate that requires the old range. Decide for each
one:

- **Keep the old pin** when the code passes the crate's types to the
  crate that blocks it, or when the old version is already in the
  build. `tiny-skia` stays on the version that the `sctk-adwaita`
  crate of winit requires for its window decorations, because
  `media-operator/display` rasterizes with that same build, and a
  second copy is a second build of it.
- **Take the new pin** when the two copies never interact, and let
  cargo build both. `base64` in `media-browser` is an example,
  because `metrics-exporter-prometheus` uses its own copy internally.

Record each kept pin as a case in `hold_of` in `toolchains/latest.sh`.
The case names the crate that blocks the pin. `lock_holders` reads
the entry of that crate in the lock file, and the row shows `held`,
with the version of the blocking crate in the hold column. When the
blocking crate no longer depends on the pinned version, the hold
expires and the row shows `behind`.

A Go module that stays back gets a case the same way, with
`module_holders`, which reads the module graph.

Report each kept pin, with the crate or module that blocks it.
