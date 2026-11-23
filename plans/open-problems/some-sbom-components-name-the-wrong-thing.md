# Some SBOM components name the wrong thing for a scanner

Open problem. The release SBOM and the records that images carry name
every component, but a few of them name it in a form that makes a
scanner skip it, or match it against the wrong project. A person who
reads the SBOM sees the component. A scanner that reads it can miss a
real advisory or report a false one.

## What was seen on 2026-11-01

- **A CPE that names another project.** buildroot gives its
  `linux-headers` package the CPE of the Linux kernel, so the record
  in the `bluetoothd` image (written by `image-sbom` from buildroot's
  SBOM) leads a scanner that matches by CPE to report kernel
  advisories against an image that ships no kernel. Since `liken`
  [milestone 104](../../liken/plans/completed/104-the-sbom-excludes-each-package-that-no-shipped-file-contains.md),
  the OS release's SBOM gives `linux-headers` the scope `excluded`,
  because no link map of a shipped program names a file of it. The
  `bluetoothd` record still lists it.
- **Guessed CPEs.** Where a record gives no CPE, syft writes about ten
  guessed ones: one for the CA bundle and about ten for each face
  model in the `library-operator-appearances` image.
- **A component inside a component.** The OpenVINO archive holds
  oneTBB, and no record lists it.
- **Partial licenses.** The source tarballs of systemd and grub in
  `pin.toml` name each package's main licenses. systemd's
  `LICENSES/README.md` names more licenses for single files.

## What was seen on 2026-11-23

- **A package that an image does not contain.** The record in the
  `bluetoothd` image keeps every target package of its buildroot
  build. buildroot builds each package that a selected recipe depends
  on, whether or not a program of the image links it. `liken`
  milestone 104 finds those packages in the OS build from the
  linker's maps, and the `bluetoothd` build writes no maps.

## What a fix must keep

The records come from `pin.toml`, from buildroot's SBOM, and from the
evidence that the build writes, such as the linker's maps that
milestone 104 reads, never from a list kept by hand, so a fix that
changes a component's identity changes the record that writes it. A
test of a fix reads the SBOM back with the pinned syft, the way a
scanner does, and shows which components it keeps.
