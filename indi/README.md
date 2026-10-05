# indi

The INDI images that `observatory-operator` runs, on
`ghcr.io/liken-sh`. [INDI](https://indilib.org/) is the device layer
of an observatory: `indiserver` starts one process for each device
driver and serves every device to clients such as KStars over one XML
protocol on TCP port 7624.

| Image | Holds |
|---|---|
| `indi` | `indiserver`, `socat`, every program in `indi-bin` (the core drivers, the simulators, and the tools), and `indi-shim` |
| `indi-simulators` | the GSC star catalog that the CCD and guide simulators draw from |
| `indi-open` | the third-party drivers that link no vendor SDK |
| `indi-zwo`, `indi-qhy`, `indi-playerone`, `indi-svbony`, `indi-atik`, `indi-fli`, `indi-sbig`, `indi-mi`, `indi-qsi`, `indi-apogee`, `indi-astroasis`, `indi-gphoto`, `indi-touptek` | one vendor SDK family each |

Each image is a library closure on `scratch`: the programs that its
list in `images/` names, every library the loader resolves for them,
and the files they read by name. `indi` is about 73 MB, where Ubuntu
with the same packages is about 1 GB. Every other image builds on
`indi` and adds its own drivers, from under 1 MB for SBIG to 344 MB for
the eleven ToupTek brands. Only `indi-simulators` has a shell, because
the CCD simulator runs `gsc` through `popen()`.

## The shim

`indiserver` starts every driver as its own child process, with no
arguments. A driver that runs in another pod needs a local program
that stands in for it. `indi-shim` is that program: each device is a
symbolic link to it, named for the address of the device pod's
`Service`, such as `ccd.observatory:7625`. The shim reads the address
from its own name, connects, and copies bytes in both directions until
either side closes. The device pod serves its driver with
`socat TCP-LISTEN:7625,reuseaddr EXEC:<driver>,pipes`.
`observatory-operator/plans/completed/03-the-topology-by-hand.md` gives the
design.

## The version and the revision

`indi` is a pinned component. Its `package.toml` states a version and
a revision, and every image publishes under the tag
`<version>-<revision>`, for example `20261005-1`.

- The **version** is the date of the snapshots that the build installs
  from: Ubuntu 26.04 from `snapshot.ubuntu.com`, and the INDI PPA from
  `snapshot.ppa.launchpadcontent.net`. The PPA itself keeps only its
  newest build of each package, so only a snapshot builds the same
  files again.
- The **revision** counts the changes to the recipe at one version.
- `[package.upstream]` states the package versions that the date
  installs. Each one is a label of every image,
  `sh.liken.upstream.<name>`, and each smoke check fails when the image
  holds other versions.

CI builds these images only when the tag is not published yet, so the
images build when someone bumps them, and at no other time.

## Bump the snapshot

1. Choose a date, and read the versions that the PPA snapshot of that
   date serves:
   `https://snapshot.ppa.launchpadcontent.net/mutlaqja/ppa/ubuntu/<date>T000000Z/dists/resolute/main/binary-amd64/Packages.gz`.
2. Set `version` to the date and `revision` to 1, and update
   `[package.upstream]`.
3. Run `make workflows` at the top of the repository.
4. Build every image with `docker buildx bake --load` and the 16
   targets of `package.toml`, and run each image's smoke check with
   its tag.

A new snapshot can carry a new SDK, or an SDK whose license file
changed. Compare each SDK directory of indi-3rdparty at the commit the
PPA built from with `sdk-licenses/`, copy each changed or new license
file, add a `license` entry to the image's list, and update the commit
under "The notices" below.

The build fails when the new release installs a third-party driver
that no list in `images/` names, or when a list names one that it no
longer installs. Add a new driver to an image's list or to
`images/unpublished`.

Search the new closure for libraries that the drivers load with
`dlopen`, which `ldd` does not list: `objdump -T` names the libraries
that import `dlopen`, and running a driver with `LD_DEBUG=libs` names
what it opens. The alignment math plugins and the gphoto2 plugins are
seeds in their lists for this reason.

## The lists

Each file in `images/` lists the seeds of one image, one per line:

- `driver <name>`: the program `/usr/bin/<name>`.
- `package <name>`: every `indi_*` program of a package.
- `seed <path>`: a program or a library, or every file of a directory.
- `sdk <name>`: every file of a vendor SDK in `indi-3rdparty-libs`.
- `data <path>`: a file or a directory, copied as it is.
- `link <path> <target>`: a symbolic link.
- `license <path>`: a vendor SDK's license file, from `sdk-licenses/`.

## The notices

Each image holds the notices that the licenses of its files ask to
travel with them. A closure copies only what the loader resolves, so
`closure.sh` also copies the copyright file of each Ubuntu and PPA
package that the closure took a file from, to
`/usr/share/doc/<package>/copyright`, and the license texts of
`/usr/share/common-licenses`. It writes
`/usr/share/doc/liken/<image>.packages`, which names each package, its
version, and its source package, with the snapshots as `deb-src`
entries, so `apt-get source` fetches the source of each one. The shim
links the Go standard library, so `indi` holds the Go license at
`/usr/share/doc/go/LICENSE`.

The copyright file of the PPA's `indi-3rdparty-libs` states one
license for the whole package and names none of the vendor SDKs in
it. `sdk-licenses/` mirrors the license file of each SDK directory in
the [indi-3rdparty](https://github.com/indilib/indi-3rdparty)
repository at commit `9d8aff3711efa824137123b007b97978aeac2688`, the
newest commit of its master branch when the PPA built the packages of
2026-10-04 that the snapshot installs. The `license` entries of
a list copy them to `/usr/share/doc/indi-3rdparty-libs/<directory>/`.
The directories of the QHY, Atik, Astroasis, and SVBONY SDKs, and of
`libflipro`, hold no license file, so their images carry none for
those SDKs.

Each smoke check runs `notices/check.sh` at the top of the repository,
which fails when a package that a list names has no copyright file in
the image, or when a `license` entry's file is missing.
