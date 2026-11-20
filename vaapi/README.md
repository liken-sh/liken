# vaapi

`ghcr.io/liken-sh/vaapi` is the base image for a program that
decodes or encodes video on the node's GPU through VA-API. It
contains the libraries that such a program opens: `libva`,
`libva-drm`, Intel's media driver, and Mesa's VA driver for AMD GPUs.
It is the [`vulkan`](../vulkan/) image with those files added, so a
program on it can also draw with Vulkan.

The image contains only libraries and their package records. It has
no program. The Intel driver is Debian's non-free build. The free
build leaves out each codec that uses Intel's prebuilt GPU kernels,
such as AV1 decode on Gen12 GPUs. No program in the repository links
the driver. libva opens it at run time, by the name that the kernel
driver reports. The comments in the `Dockerfile` give the reasons.

`vaapi` builds on `vulkan`, from the same Debian snapshot. Its layer
contains only the files that `vulkan` does not, so a node stores one
copy of each library that the two images share.
[`ffmpeg`](../ffmpeg/) builds on `vaapi`. So does the capabilities
agent of `media-operator`, which uses libva to find out what each
render node's driver can decode and encode.

## What the image contains

- **The VA-API loader**, `libva` and `libva-drm`.
- **Intel's media driver**, `iHD_drv_video.so`, from Debian's
  `non-free` component.
- **Mesa's VA driver for AMD**, `radeonsi_drv_video.so`, with the
  `libgallium` that it links.
- **The record of each package**, as in `vulkan`, in
  `/usr/share/doc/liken/vaapi.packages`.

The exact packages are in the `Dockerfile`. The upstream releases are
under `[package.upstream]` in `package.toml`. Every file is in the
image's SBOM. The image publishes under the tag
`<version>-<revision>`, where the version is the date of the Debian
snapshot. The design of the image is in `display-operator`'s
[plan 19](../display-operator/plans/completed/19-the-vaapi-and-ffmpeg-images.md).

MIT. See [LICENSE](LICENSE).
