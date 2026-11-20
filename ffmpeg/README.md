# ffmpeg

`ghcr.io/liken-sh/ffmpeg` is the base image for a program that runs
`ffmpeg` or `ffprobe`. It contains both programs, every library that
they load, and the VA-API drivers that decode on the node's GPU. Its
entrypoint is `ffmpeg`. It is the [`vaapi`](../vaapi/) image with
Debian's `ffmpeg` added.

Apart from `ffmpeg` and `ffprobe`, the image has no program and no
shell. It contains the libav libraries and their codecs. Put your own
program in an image that builds on this one, and have it run
`ffmpeg` and `ffprobe` by path.

`ffmpeg` builds on `vaapi`, from the same Debian snapshot. Its layer
contains only the files that `vaapi` and `vulkan` do not.
[`mpv`](../mpv/) builds on `ffmpeg`. So does each image that runs
`ffmpeg` in a pod, such as the capture container of
`display-operator`. Find each one by its `FROM ffmpeg` line.

CI runs `smoke/ffmpeg.sh` on the image before it publishes. The CI
runner has no GPU, so the test proves the software path and that
VA-API is compiled in. A machine with a GPU proves that the driver
loads.

The `Dockerfile` names the packages. The upstream release is under
`[package.upstream]` in `package.toml`. Every file is in the image's
SBOM. The image publishes under the tag `<version>-<revision>`, as
[`vulkan`](../vulkan/) does. The design of the image is in
`display-operator`'s
[plan 19](../display-operator/plans/completed/19-the-vaapi-and-ffmpeg-images.md).

MIT. See [LICENSE](LICENSE).
