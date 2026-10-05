# 02, Driver and simulator images

Built on 2026-10-05, and published at `20261005-1` and `-2`. The sizes
below come from a prototype on a workstation; the published images are
about 2 MB larger each, for the shim and the image's own loader cache.

## The problem

Each device pod runs one driver. The core drivers are in the `indi`
base of [plan 01](01-the-indi-base-image.md). The third-party tree,
`indi-3rdparty`, has 62 driver directories on 27 vendor library
directories, and many of those drivers link a closed vendor SDK. For
Ubuntu 26.04 the PPA publishes the whole tree as one package,
`indi-3rdparty-drivers`, with its libraries in `indi-3rdparty-libs`.
An image with all of it is about 1 GB, and a rig uses a few drivers.

## The images

Each image is the closure of its drivers, minus every file that the
`indi` base already holds, on top of the base. A family is the set of
drivers that link the same vendor SDK, found by the libraries that each
driver needs beyond the base.

| Image | Size | Drivers |
|---|---|---|
| `indi` (plan 01) | 71.2 MB | every core driver |
| `indi-simulators` | 376 MB | the GSC catalog and `gsc`, for the CCD and guide simulators |
| `indi-open` | 83.1 MB | the third-party drivers with no vendor SDK: EQMod, Celestron AUX, AZ-GTi, Star Adventurer, NexDome, Beaver, Rolloffino, AAG CloudWatcher, the weather drivers, SX, and others |
| `indi-zwo` | 76.3 MB | ASI cameras, EFW, EAF, CAA rotator, ST4 |
| `indi-qhy` | 77.3 MB | QHY cameras |
| `indi-playerone` | 72.0 MB | Player One cameras and filter wheel |
| `indi-svbony` | 76.8 MB | SVBony's own SDK |
| `indi-atik` | 92.6 MB | Atik cameras and filter wheels |
| `indi-fli` | 72.5 MB | FLI cameras, focusers, filter wheels, and Kepler |
| `indi-sbig` | 71.6 MB | SBIG |
| `indi-mi` | 71.4 MB | Moravian |
| `indi-qsi` | 71.8 MB | QSI |
| `indi-apogee` | 73.1 MB | Apogee |
| `indi-astroasis` | 71.4 MB | Astroasis Oasis focuser and filter wheel |
| `indi-gphoto` | 86.2 MB | Canon, Nikon, Sony, Fuji, and Pentax cameras through gphoto2 |
| `indi-touptek` | 415 MB | all eleven ToupTek brands: ToupTek, Altair, Bresser, Mallincam, Meade, Ogma, Omegon, StarShoot, TS, SVBony, and NNCam |

Each size includes the base layer, which every pod on a node shares.
ToupTek ships one SDK for each brand, each 20 to 64 MB, and each brand
has its own drivers. One image for the family is simpler to maintain
than eleven, and its size is accepted.

The images that a rig does not use are never pulled. A recipe exists
for an image only when a rig needs it, so the list grows from the
equipment people own.

`indi-simulators` is test equipment. It is the only image with a
shell: the CCD simulator runs `gsc` through `popen()`
(`drivers/ccd/sky_renderer.cpp`), which starts `/bin/sh`, and the
prototype drew an empty frame until the image had `dash` as `/bin/sh`.
`indi-open` carries the CA bundle of the `trust` component, because
the network weather drivers fetch over HTTPS with libcurl.

## Loads that ldd does not see

A closure from `ldd` misses every library that a program loads with
`dlopen`. Each image checks its drivers with three steps, and CI
repeats them on every bump:

1. `objdump -T` lists the libraries in the closure that import
   `dlopen`. In the prototype: `libindiAlignmentDriver`, the eleven
   ToupTek SDKs, `libEAFFocuser`, and several system libraries.
2. `strings` lists the library names each one carries. The ToupTek SDK
   names only glibc's libraries, which the base holds.
3. The driver runs in the full builder with `LD_DEBUG=libs`, and every
   library it opens is compared with the closure.

Two loads were found this way. The alignment math plugins are seeds of
the base, and gphoto2 loads its 27 camera and port plugins from
`/usr/lib/x86_64-linux-gnu/libgphoto2/` and `libgphoto2_port/`, which
are seeds of `indi-gphoto`.

A closed SDK can load a library only after it finds a camera, and with
no camera that code never runs. So an image copies every file that a
vendor's SDK installs under its own library name, not only the files
that `ldd` reports. The first real camera of each family is the last
test.

## The driver list of each image

The prototype chose each image's drivers with a pattern on the binary
names. Each recipe lists its drivers by name instead. When an INDI bump
adds a driver, the build reports every third-party driver that no
image lists, so a new driver is a decision, not a silent addition.

## How we test it

- Every binary in every image resolves every library with the image's
  own loader. The prototype passed for every image.
- Every driver of every image starts under `indiserver` with no
  hardware and stays up, with no restart and no loader error. The
  prototype passed for every driver. That is weak evidence for a
  camera driver, which defines no device until it finds a camera.
- The simulator image draws a star field. The prototype's frame had
  172 star pixels, and `solve-field` solved it at RA 83.56°,
  Dec −5.41°.
- Each image states its license. An SDK image carries one vendor's
  license, and `indi-open` carries no closed binary.

## Upstream issues

- [indi-3rdparty#1332](https://github.com/indilib/indi-3rdparty/issues/1332)
  and [PR #1327](https://github.com/indilib/indi-3rdparty/pull/1327):
  QHY firmware moved to `/usr/lib/firmware/qhy`, and the udev `fxload`
  rule pointed at the wrong path for a while. The firmware loads on the
  host, so the QHY image depends on `liken` for it.
- [indi#1796](https://github.com/indilib/indi/issues/1796): the upstream
  udev rules give serial adapters mode `0666`. `liken` does not copy
  them.
- [indi#2412](https://github.com/indilib/indi/pull/2412) and
  [indi#2483](https://github.com/indilib/indi/pull/2483): the CCD
  simulator's drawing moved into `SkyRenderer` in June 2026, and a
  regression followed. The star count of the smoke test can change
  with an INDI bump.
- [indi#2395](https://github.com/indilib/indi/issues/2395): the CCD
  simulator reset its resolution to 1280 by 1024.
- [indi#2032](https://github.com/indilib/indi/pull/2032): the CCD
  simulator takes its focal length from `SCOPE_INFO`, so it draws an
  empty frame at the default of 0.

## References

- `indi-3rdparty`: <https://github.com/indilib/indi-3rdparty>
- The driver catalog: `/usr/share/indi/drivers.xml`, 291 devices
- The CCD simulator: `drivers/ccd/ccd_simulator.cpp` and
  `drivers/ccd/sky_renderer.cpp` in `indilib/indi`
- [Root plan 74](../../../plans/74-astrophotography.md), "Images"
