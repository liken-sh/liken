package main

import (
	"fmt"
	"maps"
	"slices"

	"github.com/BurntSushi/toml"
)

// manifestComponents reads one pin.toml and makes a component of each
// entry. Root AGENTS.md states the form of a manifest under "Pins and
// downloads", and `ci pins` checks it, so this reader takes each field
// as it is.
//
// An entry names one file that a fetch downloads, so its component is
// of type file, and its hash is the sha256 of that download. The purl
// and the license are the ones that a person wrote for the upstream,
// which is why a manifest's component wins over syft's guess at the
// same program. The URL becomes the distribution reference, with the
// same hash, so a reader can download the file and check it.
func manifestComponents(path string) ([]map[string]any, error) {
	var entries map[string]map[string]string
	if _, err := toml.DecodeFile(path, &entries); err != nil {
		return nil, fmt.Errorf("reading the manifest %s: %w", path, err)
	}
	var components []map[string]any
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		entry := entries[name]
		scope := entry["scope"]
		if scope == "" {
			scope = "required"
		}
		digest := []hash{{Alg: "SHA-256", Content: entry["sha256"]}}
		c := map[string]any{
			"bom-ref":  "pin:" + name,
			"type":     "file",
			"name":     name,
			"version":  entry["version"],
			"scope":    scope,
			"purl":     entry["purl"],
			"hashes":   digest,
			"licenses": []license{{Expression: entry["license"]}},
			"externalReferences": []reference{
				{Type: "distribution", URL: entry["url"], Hashes: digest},
			},
		}
		addProperty(c, sourceProperty, "manifest")
		components = append(components, c)
	}
	return components, nil
}
