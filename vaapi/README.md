# vaapi

`ghcr.io/liken-sh/vaapi` is the [`vulkan`](../vulkan/) image plus the
VA-API loader, the Intel media driver, and Mesa's VA driver for AMD,
for a program that decodes video on the node's GPU. [`ffmpeg`](../ffmpeg/) builds on it.

## What the image holds

`vaapi-closure.sh` names four seeds: `libva.so.2`, `libva-drm.so.2`,
`iHD_drv_video.so`, the Intel media driver, and `radeonsi_drv_video.so`,
Mesa's VA driver for AMD. libva opens a driver by the name the kernel
driver reports, so no program's library graph reaches it, and it is a
seed. The script collects the seeds and every library they load, and
then leaves out every file that the `vulkan` image already holds, so
this image's layer carries only what the base lacks.

The Intel driver is Debian's `intel-media-va-driver-non-free`, from the
snapshot's `non-free` component. Debian's free build,
`intel-media-va-driver`, leaves out every codec whose driver path runs
Intel's prebuilt GPU kernels. On Gen12 GPUs, Tiger Lake and Alder Lake
among them, that includes AV1 decode, along with VC-1 decode and the
full H.264 and HEVC encoders. The kernels are binaries without their
source, under the same Expat license as the rest of the driver, and
that missing source is why Debian files the package as non-free. libva
opens the driver at run time, and no program in this repository links
it.

On Debian, Mesa's VA driver is a link into `libgallium`, which the
`vulkan` tree does not hold. It is the largest file in this layer, 41
MB, and the non-free Intel driver is 41 MB where the free one is 18 MB.
The layer measured 85 MB on the snapshot of 2026-09-28, where it was
19 MB with the free Intel driver alone.

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
