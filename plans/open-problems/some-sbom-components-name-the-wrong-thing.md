# Some SBOM components name the wrong thing for a scanner

Open problem. The release SBOM and the records that images carry name
every component, but a few of them name it in a form that makes a
scanner skip it, or match it against the wrong project. A person who
reads the SBOM sees the component. A scanner that reads it can miss a
real advisory or report a false one.

## What was seen on 2026-11-01

- **A pin's type.** `toolchains/manifest` gives a `pin.toml` entry
  with no `type` the CycloneDX type `file` (`defaultType` in
  `toolchains/manifest/manifest.go`), and the release SBOM keeps it.
  syft's CycloneDX decoder keeps only the types `application`,
  `framework`, `library`, `machine-learning-model`, and `firmware`, so
  syft drops k3s, the kernel, and every other such pin when it reads
  the release SBOM back. Whether Trivy drops them too was not checked.
- **A CPE that names another project.** buildroot gives its
  `linux-headers` package the CPE of the Linux kernel, so the record
  in the `bluetoothd` image (written by `image-sbom` from buildroot's
  SBOM) leads a scanner that matches by CPE to report kernel
  advisories against an image that ships no kernel.
- **Guessed CPEs.** Where a record gives no CPE, syft writes about ten
  guessed ones: one for the CA bundle and about ten for each face
  model in the `library-operator-appearances` image.
- **A component inside a component.** The OpenVINO archive holds
  oneTBB, and no record lists it.
- **Partial licenses.** The source tarballs of systemd and grub in
  `pin.toml` name each package's main licenses. systemd's
  `LICENSES/README.md` names more licenses for single files.

## What a fix must keep

The records come from `pin.toml` and from buildroot's SBOM, never from
a list kept by hand, so a fix that changes a component's identity
changes the record that writes it. A test of a fix reads the SBOM back
with the pinned syft, the way a scanner does, and shows which
components it keeps.
