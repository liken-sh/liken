# The PHD2 image carries OpenCV's backends

Open problem. `ghcr.io/liken-sh/indi-phd2:20261005-4` is 513 MB, and
`indi` is 73 MB. Most of the difference is OpenCV's video and image
backends, which the guider never uses.

## What happens

`indi-phd2` is a library closure, like every `indi` image: it holds
PHD2, every library the loader resolves for it, and the files it reads
by name. PHD2 from `ppa:pch/phd2` links three OpenCV modules, and
Ubuntu 26.04 builds them with every backend:

| Module | What it pulls in |
|---|---|
| `libopencv_videoio` | FFmpeg with its encoders (x265, SVT-AV1, aom, vpx, codec2), GStreamer, `libgphoto2`, `libdc1394` |
| `libopencv_imgcodecs`, through `videoio` | GDAL with MySQL, SpatiaLite, HDF5, and poppler; OpenEXR; GDCM |
| `libopencv_core` | OpenBLAS (37 MB) and TBB |

PHD2 uses `videoio` for webcam guide cameras. The guider reaches its
camera through INDI, so no frame passes through those backends.

## Options considered

- **Keep the image as it is.** This is the current choice. The image
  works, and a node pulls it once for each `indi` revision.
- **Build OpenCV in the `phd2` stage.** Build OpenCV 4.10 with only
  `core`, `imgproc`, `imgcodecs`, and `videoio`, and with every backend
  off. The sonames stay the same, so the PPA's PHD2 binary links
  unchanged. GTK, wxWidgets, and ICU remain. The size after the change
  was not measured.
- **Build PHD2 from source without OpenCV's camera support.** This
  saves more, and adds a second source build to pin.

Return to this when the image's size matters on a node, such as a
small disk or a slow link to the registry.
