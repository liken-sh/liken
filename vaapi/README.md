# vaapi

`ghcr.io/liken-sh/vaapi` is the base image for a program that
decodes or encodes video on the node's GPU through VA-API. It
contains the libraries that such a program opens: the VA-API loader,
Intel's media driver, and Mesa's VA driver for AMD GPUs. It is the
[`vulkan`](../vulkan/) image with those files added, so a program on
it can also draw with Vulkan.

The image contains only libraries and their package records. It has
no program. The Intel driver is Debian's non-free build. The free
build leaves out each codec that uses Intel's prebuilt GPU kernels,
such as AV1 decode on Gen12 GPUs. No program in the repository links
the driver. libva opens it at run time, by the name that the kernel
driver reports. The comments in the `Dockerfile` give the reasons.

`vaapi` builds on `vulkan`, from the same Debian snapshot. Its layer
contains only the files that `vulkan` does not, so a node stores one
copy of each library that the two images share.
[`ffmpeg`](../ffmpeg/) builds on `vaapi`. So do images that use libva
directly, such as the capabilities agent of `media-operator`, which
finds out what each render node's driver can decode and encode.

The `Dockerfile` names the packages. The upstream releases are under
`[package.upstream]` in `package.toml`. Every file is in the image's
SBOM. The image publishes under the tag `<version>-<revision>`, as
[`vulkan`](../vulkan/) does. The design of the image is in
`display-operator`'s
[plan 19](../display-operator/plans/completed/19-the-vaapi-and-ffmpeg-images.md).

MIT. See [LICENSE](LICENSE).
