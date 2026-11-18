---
title: Drivers and images
weight: 15
---

# Drivers and images

A device sets its INDI driver in `spec.driver.name`, and the operator
runs the driver from an image that the `indi` build publishes on
`ghcr.io/liken-sh`. Each operator build pins the tag of those images,
so it runs the driver versions that it was tested with.
[Describe your equipment](/docs/guides/describe-your-equipment/)
shows how to declare a device.

The operator chooses the image from the driver's name, in this order:

1. A device with `spec.driver.image` runs that image, as written.
2. A vendor's driver runs from the image for that vendor in the
   table below.
3. A simulator, a driver whose name starts with `indi_simulator_`,
   runs from `indi-simulators`, which adds the star catalog that the
   camera simulators use.
4. Every other driver runs from `indi`, which holds INDI's own
   drivers: the mounts, focusers, domes, weather stations, and the
   rest of the `indi-bin` package. `indi_lx200generic`,
   `indi_celestron_gps`, and `indi_moonlite` are among them.

| Image | Drivers |
|---|---|
| `indi-open` | the third-party drivers that link no vendor SDK, such as `indi_eqmod_telescope`, `indi_rolloffino`, `indi_aagcloudwatcher_ng`, and the Atik EFW wheel |
| `indi-zwo` | ZWO cameras, EFW filter wheels, EAF focusers, the CAA rotator, and the USB-to-ST4 adapter |
| `indi-qhy` | QHY cameras |
| `indi-playerone` | Player One cameras and filter wheel |
| `indi-svbony` | SVBony cameras on SVBony's own SDK |
| `indi-touptek` | the brands on the ToupTek SDK: ToupTek, Altair, Bresser, Mallincam, Meade, NNCam, Ogma, Omegon, StarShoot, SVBony, and TS |
| `indi-atik` | Atik cameras and filter wheels |
| `indi-fli` | Finger Lakes Instrumentation cameras, focusers, and filter wheels, and the Kepler cameras |
| `indi-sbig` | SBIG cameras |
| `indi-mi` | Moravian Instruments cameras and filter wheels |
| `indi-qsi` | Quantum Scientific Imaging cameras |
| `indi-apogee` | Apogee cameras and filter wheels |
| `indi-astroasis` | the Astroasis Oasis focuser and filter wheel |
| `indi-gphoto` | Canon, Nikon, Sony, Fuji, and Pentax cameras through gphoto2 |

The lists in [`indi/images/`](https://github.com/liken-sh/liken/tree/main/indi/images)
give the exact drivers of each image. The drivers of the Fishcamp
and iNova PLx cameras, which their vendors no longer sell, and of the
LimeSDR radio receiver are in no image. A device that uses one of
them must set `spec.driver.image`. Without it, the reservation's step
that starts the device fails, and its message says that no image
contains the driver.

A driver in an image does not mean that a pod can reach the driver's
hardware. Most of the cameras in the table
connect through their vendor's library with no kernel driver, and
`liken` publishes such a device as a whole, with its USB node. A QHY
camera that starts with no firmware needs `firmware-operator` to load
the firmware first, and a QHY camera on an FX3 chip that needs
firmware does not work yet.
[Connect USB equipment](/docs/guides/connect-usb-equipment/#what-can-reach-a-pod)
has the details.

## Your own image

`spec.driver.image` runs any image. The image must:

* contain `/usr/bin/socat`, because the device's pod serves the driver
  with `socat` on port 7625
* contain the driver on `PATH`, under the name in `spec.driver.name`
* run as user 1000, with a read-only root filesystem and a writable
  `/tmp`

The images of the `indi` build contain only the programs they run and
the libraries that those programs load. Only `indi-simulators` and
`indi-phd2` have a shell. The
[`indi` README](https://github.com/liken-sh/liken/tree/main/indi)
describes how they are built.
