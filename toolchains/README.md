# toolchains

`toolchains` contains the pins and the pin library that every build in
the repository shares. An outside image is one that this repository
does not build. Each outside image that a build starts from or runs is
pinned once, here. So when you bump an image such as `golang` or
`debian`, one commit changes it for every Dockerfile, script, and
workflow that uses it.

`toolchains` sets which outside image each build uses. It also has
the shell functions that every `fetch.sh` and `latest.sh` calls to
download a pinned file and to report or move a pin, and the reader of
the pin manifests. The pins of each component's own downloads are not
here. Each `VERSION`, `fetch.sh`, and `pin.toml` is in the directory
of the component that uses it.

Each component whose Dockerfile uses an image from `images.toml`
depends on `toolchains`. So a change to `images.toml` counts as a
change to each of those components. `ci` writes each image reference
into the bake targets. `ci pins` fails when a file uses an image from
the table at a different tag. The program that writes an OS
release's SBOM reads the pin manifests through `manifest/`, and so
does the build of each image that ships a pinned file.

## The parts

- **`images.toml`** pins each outside image. The `[build]` table lists
  the images that a Dockerfile starts from or copies from, such as
  `golang` and `debian`. It also lists the tools that CI runs as
  images, such as `cosign` and `grype`. Each of these entries has a
  tag and a digest. The `[run]` table lists the images that a
  manifest, a guide, a skill, or a test refers to. Each of these
  entries has only a tag.
- **`pin.sh`** is the shell library that every `fetch.sh` and
  `latest.sh` sources. Its functions check that the needed tools are
  installed, download a pinned file and check its digest, print a row
  of the `make versions` report, and write a bump into the files that
  record the pin.
- **`latest.sh`** compares each image in `images.toml`, the Go and Rust
  toolchains, and the Go modules and Rust crates of every `go.mod` and
  `Cargo.toml` with their upstreams, and reports the result.
- **`cargo-auditable/`** pins cargo-auditable. Every Rust image build
  runs it, so that the binary records the crates it links.
- **`manifest/`** is the Go module that reads a `pin.toml` and returns
  each entry as a CycloneDX component. Its command `image-sbom` writes
  the file `/usr/share/doc/liken/<image>.cdx.json` into an image, and
  syft reads that file.

The `pins` skill has the rules for a pin and for a download. Root
plan 83 sets the rule that each pin follows the newest upstream
release.

MIT. See [LICENSE](LICENSE).
