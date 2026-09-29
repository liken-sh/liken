# ffmpeg

`ghcr.io/liken-sh/ffmpeg` is the [`vaapi`](../vaapi/) image plus
`ffmpeg` and `ffprobe` and every library they load. Its entrypoint is
`ffmpeg`. [`mpv`](../mpv/) builds on it, and so do the images that run
ffmpeg: the capture container of `display-operator`, the API of
`media-operator`, and the file facts of `library-operator`.

## What the image holds

`ffmpeg-closure.sh` names the two programs as seeds, collects every
library they load, and then leaves out every file that the images
under this one already hold. The layer carries the libav libraries and
their codecs, and the image decodes on the node's GPU through the
VA-API tree under it.

`smoke/ffmpeg.sh` runs the image before it ships: it encodes one
second of a test pattern, runs `ffprobe`, and checks that `ffmpeg`
lists the `vaapi` hardware acceleration.

## The version and the revision

`ffmpeg` is a pinned component. Its `package.toml` states a version and a
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

    docker buildx bake ffmpeg --load
