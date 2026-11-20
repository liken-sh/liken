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
[`mpv`](../mpv/) builds on `ffmpeg`. So do the images that run
`ffmpeg` in a pod:

- the capture container of `display-operator`
- the API of `media-operator`, which muxes each capture
- the images of `library-operator` that probe a video, make its seek
  bar images, read the text on screen at its end, and find the
  credited people on screen

## What the image contains

- **`ffmpeg` and `ffprobe`**, from Debian's `ffmpeg` package.
- **The libav libraries** and every library that they load, such as
  the codec libraries.
- **The record of each package**, as in `vulkan`, in
  `/usr/share/doc/liken/ffmpeg.packages`.

`smoke/ffmpeg.sh` tests the image before it publishes. It encodes one
second of a test pattern, runs `ffprobe`, and checks that `ffmpeg`
lists the `vaapi` hardware acceleration. The CI runner has no GPU, so
the test proves the software path and that VA-API is compiled in. A
machine with a GPU proves that the driver loads.

The exact packages are in the `Dockerfile`. The upstream release is
under `[package.upstream]` in `package.toml`. Every file is in the
image's SBOM. The image publishes under the tag
`<version>-<revision>`, where the version is the date of the Debian
snapshot. The design of the image is in `display-operator`'s
[plan 19](../display-operator/plans/completed/19-the-vaapi-and-ffmpeg-images.md).

MIT. See [LICENSE](LICENSE).
