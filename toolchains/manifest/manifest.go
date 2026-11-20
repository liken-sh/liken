// Package manifest reads a pin's manifest, pin.toml, and gives each
// entry as a CycloneDX component.
//
// A manifest names each file that a pin downloads and that a release
// or an image ships, with the facts that an SBOM needs about the file.
// Root AGENTS.md states its form under "Pins and downloads",
// toolchains/pin.sh writes and reads it, and `ci pins` checks it, so
// this reader takes each field as it is. Two SBOMs read manifests:
// liken/releases/sbom writes the SBOM of an OS release, and image-sbom
// writes the record that an image carries for syft to read. Both read
// the manifests through this package, so a field means the same thing
// in both.
package manifest

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// defaultType is the CycloneDX type of an entry that names none: one
// file that a fetch downloads. An entry that an image ships names a
// type that syft keeps, because syft drops a component of the type
// file or data when it reads an SBOM inside an image.
const defaultType = "file"

type hash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

type license struct {
	Expression string `json:"expression"`
}

// A pedigree states that the shipped program is the upstream release
// with changes applied. CycloneDX names each change a patch, and the
// commit it came from a commit.
type pedigree struct {
	Commits []commit `json:"commits"`
	Patches []patch  `json:"patches"`
}

type commit struct {
	UID string `json:"uid"`
	URL string `json:"url"`
}

type patch struct {
	Type string `json:"type"`
	Diff diff   `json:"diff"`
}

type diff struct {
	URL string `json:"url"`
}

type reference struct {
	Type   string `json:"type"`
	URL    string `json:"url"`
	Hashes []hash `json:"hashes,omitempty"`
}

// Components reads one pin.toml and makes a component of each entry,
// in the order of the entries' names.
//
// The component's hash is the sha256 of the download. The purl, the
// CPE, and the license are the ones that a person wrote for the
// upstream, which is why a manifest's component wins over syft's guess
// at the same program. The URL becomes the distribution reference, with
// the same hash, so a reader can download the file and check it. The
// bom-ref is pin:<entry>, the name by which a claim in the release's
// SBOM names the component.
//
// An entry's backports field lists, separated by spaces, the GitHub
// URL of each upstream commit that the build applies to the release.
// The component then carries a pedigree with one commit and one patch
// of the type backport for each, and the patch's diff is the commit's
// .patch form. So the SBOM does not state the stock release where the
// build ships a patched one, and the version stays the release's own.
//
// A component is a map, not a struct, because the release's SBOM adds
// properties to it and copies it with every field it holds.
func Components(path string) ([]map[string]any, error) {
	var entries map[string]map[string]string
	if _, err := toml.DecodeFile(path, &entries); err != nil {
		return nil, fmt.Errorf("reading the manifest %s: %w", path, err)
	}
	var components []map[string]any
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		entry := entries[name]
		digest := []hash{{Alg: "SHA-256", Content: entry["sha256"]}}
		c := map[string]any{
			"bom-ref":  "pin:" + name,
			"type":     orDefault(entry["type"], defaultType),
			"name":     name,
			"version":  entry["version"],
			"scope":    orDefault(entry["scope"], "required"),
			"purl":     entry["purl"],
			"hashes":   digest,
			"licenses": []license{{Expression: entry["license"]}},
			"externalReferences": []reference{
				{Type: "distribution", URL: entry["url"], Hashes: digest},
			},
		}
		if cpe := entry["cpe"]; cpe != "" {
			c["cpe"] = cpe
		}
		if backports := strings.Fields(entry["backports"]); len(backports) > 0 {
			c["pedigree"] = backportPedigree(backports)
		}
		components = append(components, c)
	}
	return components, nil
}

// backportPedigree gives each commit URL as a commit, named by the
// commit's id, the last element of the URL, and as a backport patch.
func backportPedigree(urls []string) pedigree {
	var p pedigree
	for _, url := range urls {
		p.Commits = append(p.Commits, commit{UID: path.Base(url), URL: url})
		p.Patches = append(p.Patches, patch{Type: "backport", Diff: diff{URL: url + ".patch"}})
	}
	return p
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
