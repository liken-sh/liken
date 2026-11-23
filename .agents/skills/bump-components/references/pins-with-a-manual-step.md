# Pins with a manual step

Each of these scripts needs a step that no section of the skill gives.

- **`liken/dev-cluster/firmware/capsule/latest.sh`** reports edk2
  and the Ubuntu snapshot of the builder of the lab's capsule
  firmware. Each move changes the compiler that builds the firmware,
  so the script has no `--bump`. Edit `build.sh` or the `Dockerfile`
  by hand, and boot the lab with `FIRMWARE=capsule`. Do the same
  after a move of the `ubuntu` entry of `toolchains/images.toml`.
- **`liken/releases/sbom/schema/latest.sh`** writes the CycloneDX
  schema files that the SBOM of the OS release is checked against. A
  new minor version changes the `specVersion` that the SBOM declares,
  so run `make -C liken test-go` after the bump.
- **`liken/verify/latest.sh`** writes Sigstore's trusted root, which
  `liken verify` and `liken sbom` check every signature against. The
  bump reads the root through Sigstore's TUF repository with the
  pinned cosign, so it needs docker. Read its diff for each key that
  was added or removed.
- **`transect/crates/transect/src/web/static/latest.sh`** writes the
  htmx files that `transect` serves. It stops at a new major version
  of htmx, which needs a port of the templates and the scripts, and
  [`htmx.md`](htmx.md) gives the port and its proof.
