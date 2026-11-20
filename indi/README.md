# indi

The INDI images give a [`liken`](https://liken.sh/) cluster the
drivers for an observatory's equipment: mounts, cameras, focusers,
filter wheels, domes, and more. The drivers come from the core of
[INDI](https://indilib.org/) and from its third-party drivers. The
images also include the INDI simulators and PHD2, the guider.
`indiserver` starts one process for each device driver. It serves
every device to clients such as KStars over one XML protocol on TCP
port 7624.

This component is a pinned set of images on `ghcr.io/liken-sh`, not
an operator. It contains the recipe of each image, and the shim that
connects `indiserver` to a driver in another pod. It does not decide
which driver runs where, or when. `observatory-operator` does that.

`observatory-operator` runs these images. It starts each device's
driver from the image that contains that driver. It starts the
telescope's `indiserver` from `indi`, and PHD2 from `indi-phd2`
beside the `weston` image. `firmware-operator` runs `fxload` from
`indi-qhy` to load the firmware of a QHY camera. The entrypoint of
the `indi` image is `indiserver`, so you can also run it with Docker
or in a pod of your own.

## The images

- `indi` contains `indiserver`, `socat`, every program of `indi-bin`
  (the core drivers, the simulators, and the tools), and `indi-shim`.
- Each vendor image, such as `indi-zwo` or `indi-qhy`, adds the
  third-party drivers of one vendor SDK family. It also adds the SDK,
  and the SDK's license file when indi-3rdparty has one. `indi-qhy`
  also contains `fxload`, the QHY firmware files, and `libqhy`'s udev
  rules. `observatory-operator` generates its QHY `Firmware` objects
  from those udev rules.
- `indi-open` adds the third-party drivers that link no vendor SDK.
- `indi-simulators` adds the GSC star catalog. The CCD and guide
  simulators draw their stars from it.
- `indi-phd2` adds [PHD2](https://openphdguiding.org/). PHD2 links the
  same `libindiclient` as the server that it connects to, and serves
  its event API on TCP port 4400.

`package.toml` lists every image. Each list in `images/` names the
programs, libraries, and files of one image. The drivers that no
image publishes are listed in `images/unpublished`.

## How the images are built

Each image is a library closure: an image built `FROM scratch` with no
package manager. It contains only the programs that its list names,
every library that the loader resolves for those programs, and the
files that the programs open by name. `indi` is the base closure.
Every other image builds on it and adds only its own files. Only
`indi-simulators` and `indi-phd2` have a shell. The CCD simulator
needs one because it runs `gsc` through `popen()`, and PHD2 needs one
because its entrypoint is a script. Each image includes the copyright
files and the dpkg records of the packages that it copied files from,
so a scanner can list its packages.

Everything installs from dated snapshots of Ubuntu, the INDI PPA, and
the PHD2 PPA. Snapshots are needed because a PPA keeps only its
newest build of each package. These entries record what a build
installs:

- `version` in `package.toml` is the date of the snapshots.
- `snapshot.sha256` pins the `InRelease` file of each suite at that
  date.
- `[package.upstream]` lists the package versions that the date
  installs.

Every image publishes under the tag `<version>-<revision>`. CI builds
the images only when that tag is new.

## The shim

`indiserver` starts every driver as its own child process, with no
arguments. When a driver runs in another pod, `indi-shim` takes its
place. Each device is a symbolic link to the shim, named for the
address of the device pod's `Service`. The shim connects to that
address and copies bytes in both directions. In the server's pod, the
shim also runs `indiserver`. When the server's list of devices
changes, the shim starts and stops drivers on the running server. The
package comments in `shim/` describe the design.

`observatory-operator`'s
[plan 01](../observatory-operator/plans/completed/01-the-indi-base-image.md)
and
[plan 02](../observatory-operator/plans/completed/02-driver-and-simulator-images.md)
record the design of the images and the sizes measured.
