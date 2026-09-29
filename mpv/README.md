# mpv

`ghcr.io/liken-sh/mpv` is the [`ffmpeg`](../ffmpeg/) image plus `mpv`
and every library and data file that it opens by name. Its entrypoint
is `mpv`. The player image of `media-operator` builds on it.

## What the image holds

`mpv-closure.sh` names `mpv` and the PipeWire client modules and SPA
plugins that its audio output loads by name, found in a traced
playback run. It collects them and every library they load, the
PipeWire client configuration, and the fontconfig configuration that
libass reads. It then leaves out every file that the images under this
one already hold, so the layer carries no second copy of the libav
libraries or the VA-API driver.

`smoke/mpv.sh` runs the image before it ships: it prints the version,
and it decodes five frames of a test pattern to no display and no
audio.

## The version and the revision

`mpv` is a pinned component. Its `package.toml` states a version and a
revision, and the image publishes under the tag `<version>-<revision>`,
for example `20260928-1`.

- The **version** is the date of the Debian snapshot that the build
  installs from, as `YYYYMMDD`. Every base on Debian uses the same
  date, so a library that two bases hold is the same file in both.
- The **revision** counts the changes to the recipe at one version. It
  starts at 1 for a new version. Any change to the build that keeps
  the version needs the next revision: a change to the `Dockerfile`, to
  a script, to the Debian image digest, or to `package.toml`.

The recipe also covers the tag of each base in `[depends]`. So a new
revision of a base under this one changes this one's recipe too, and
this one needs a new revision in the same commit. CI fails and names
each component that needs one. A published tag never changes.

## Bump the snapshot

All five bases move to a new snapshot together. The steps are in the
[`vulkan` README](../vulkan/README.md#bump-the-snapshot), and the
`bump-components` skill under `.agents/skills` holds the whole
procedure.

## Build

The bake file at the top of the repository builds this image, from
the top of the repository:

    docker buildx bake mpv --load
