# The sources inside operator images

The bluetoothd image and the appearances image fetch upstream
releases in their Dockerfiles. A `latest.sh` reports and bumps each
of them.

## The bluetoothd image

The bluetoothd image builds its static daemons with buildroot, from
`liken/userland/bluetoothd.config`. So `liken/userland/latest.sh`
reports BlueZ and dbus.

- `--bump bluez` writes the newest BlueZ release into
  `liken/userland/bluetooth/bluez-static/`.
- `--bump dbus` sets, in `versions.mk`, the newest release of a
  stable series of dbus. A stable series has an even minor version.

Then run `docker buildx bake bluetoothd`. The build stops when a
patch in `liken/userland/bluetooth/bluez-static/` no longer applies,
and the `README.md` in that directory says what to do.

Read the BlueZ release notes for changes to the D-Bus API and to the
mgmt socket, because the operator uses both.

## The appearances image

`library-operator/latest.sh --bump` moves OpenVINO within the
archive channel that the pin names, such as `ubuntu24`. When the row
shows `wrong channel`, Intel publishes an archive for a newer Ubuntu
release. To move to it, edit the URL in `Dockerfile.appearances` and
`appearances/README.md` by hand, and then run the `--bump`.

`--bump compute-runtime` moves the OpenCL driver, gmmlib from the
same release, and the release of the graphics compiler that the
release text of the runtime names.

Both bumps write URLs and checksums into `Dockerfile.appearances`.
The OpenVINO bump also writes the same values, with the purl and the
CPE, into `library-operator/pin.toml`, which the image's SBOM record
reads.

`appearances/inference-closure.sh` lists the versioned library files
of each runtime, so change those names by hand. Then run
`docker buildx bake library-operator-appearances`.
