// The reader of a pin's manifest, pin.toml, that gives each entry as a
// CycloneDX component. liken/releases/sbom imports it for the SBOM of
// an OS release, and image-sbom runs in an image build to write the
// record that the image carries, so the two SBOMs read each manifest
// the same way.
module github.com/liken-sh/liken/toolchains/manifest

go 1.27.3

require github.com/BurntSushi/toml v1.6.0
