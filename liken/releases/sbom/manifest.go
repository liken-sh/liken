package main

import "github.com/liken-sh/liken/toolchains/manifest"

// manifestComponents reads one pin.toml and makes a component of each
// entry, with the property that names the manifest as its source.
// toolchains/manifest reads the entries, the same reader that writes
// the record of an image, so a field of a manifest means the same in
// both SBOMs. An entry that names no type is one file that a fetch
// downloads, of the CycloneDX type file.
func manifestComponents(path string) ([]map[string]any, error) {
	components, err := manifest.Components(path)
	if err != nil {
		return nil, err
	}
	for _, c := range components {
		addProperty(c, sourceProperty, "manifest")
	}
	return components, nil
}
