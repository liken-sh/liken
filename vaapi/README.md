# vaapi

`ghcr.io/liken-sh/vaapi` is the [`vulkan`](../vulkan/) image plus the
VA-API loader and the Intel media driver, for a program that decodes
video on the node's GPU. [`ffmpeg`](../ffmpeg/) builds on it.

## What the image holds

`vaapi-closure.sh` names three seeds: `libva.so.2`, `libva-drm.so.2`,
and `iHD_drv_video.so`, the Intel media driver. libva opens the driver
by the name the kernel driver reports, so no program's library graph
reaches it, and it is a seed. The script collects the seeds and every
library they load, and then leaves out every file that the `vulkan`
image already holds, so this image's layer carries only what the base
lacks.

Mesa's VA driver for AMD is left out. On Debian it is a link into
libgallium, which the `vulkan` tree does not hold, so the seed would
add 40 MB for a card that no `liken` machine decodes on yet. A machine
with AMD graphics decodes in software.

## The version and the revision

`vaapi` is a pinned component. Its `package.toml` states a version and a
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

    docker buildx bake vaapi --load
