---
title: Drivers and images
weight: 15
---

# Drivers and images

A device names its INDI driver in `spec.driver.name`, and the
operator runs the driver from an image that the `indi` build
publishes on `ghcr.io/liken-sh`. Each operator build pins the tag of
those images, so the drivers it runs are the ones it was tested with.

The operator picks the image from the driver's name:

1. A device with `spec.driver.image` runs that image, as written.
2. A vendor's driver runs from its vendor's image, in the table below.
3. A simulator, a driver whose name starts with `indi_simulator_`,
   runs from `indi-simulators`, which adds the star catalog that the
   camera simulators draw from.
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
| `indi-touptek` | the eleven brands on the ToupTek SDK: ToupTek, Altair, Bresser, Mallincam, Meade, NNCam, Ogma, Omegon, StarShoot, SVBony, and TS |
| `indi-atik` | Atik cameras and filter wheels |
| `indi-fli` | Finger Lakes Instrumentation cameras, focusers, and filter wheels, and the Kepler cameras |
| `indi-sbig` | SBIG cameras |
| `indi-mi` | Moravian Instruments cameras and filter wheels |
| `indi-qsi` | Quantum Scientific Imaging cameras |
| `indi-apogee` | Apogee cameras and filter wheels |
| `indi-astroasis` | the Astroasis Oasis focuser and filter wheel |
| `indi-gphoto` | Canon, Nikon, Sony, Fuji, and Pentax cameras through gphoto2 |

Each image's list in the repository names its drivers exactly:
[`indi/images/`](https://github.com/liken-sh/liken/tree/main/indi/images).
Three third-party drivers are in no image: the Fishcamp and iNova PLx
cameras, which their vendors no longer sell, and the LimeSDR radio
receiver. A device that names one of them must name an image in
`spec.driver.image`. Without one, the reservation's step that starts
the device fails, and its message says that no image holds the
driver.

An image that holds a driver does not mean the driver's hardware can
reach a pod. Most of the cameras in the table connect through their
vendor's library with no kernel driver, and `liken` does not publish
such devices yet. [Connect USB equipment](/docs/guides/connect-usb-equipment/#what-can-reach-a-pod)
gives the details.

## Your own image

`spec.driver.image` runs any image. The image must:

* hold `/usr/bin/socat`, because the device's pod serves the driver
  with `socat` on port 7625
* hold the driver on `PATH`, under the name in `spec.driver.name`
* run as user 1000, with a read-only root filesystem and a writable
  `/tmp`

The images of the `indi` build hold only the programs they run and the
libraries those programs load. Only `indi-simulators` and `indi-phd2`
have a shell. The
[`indi` README](https://github.com/liken-sh/liken/tree/main/indi)
describes how they are built.
