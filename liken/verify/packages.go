package verify

// This file reads the two kinds of SBOM that `liken` publishes. An OS
// release carries a CycloneDX document, which releases/sbom writes,
// and an image carries an SPDX document, which buildx's syft scanner
// writes into the image's attestation. A person who lists either one
// wants the same three facts about each package: its name, its
// version, and its package URL.

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// A Package is one entry of an SBOM.
type Package struct {
	Name    string
	Version string
	PURL    string
}

// cycloneDX is the part of a CycloneDX document that the check reads.
type cycloneDX struct {
	Components []struct {
		Name       string `json:"name"`
		Version    string `json:"version"`
		PURL       string `json:"purl"`
		Properties []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"properties"`
	} `json:"components"`
}

// sourceProperty is the property in which releases/sbom records where
// a component came from: a pin's manifest, buildroot's SBOM, or syft.
const sourceProperty = "liken:source"

// A Summary counts the components of a release's SBOM, in all and by
// the source that releases/sbom records for each one.
type Summary struct {
	Components int
	BySource   map[string]int
}

// String gives the summary as one line, with the sources in name
// order: "744 components: 64 buildroot, 23 manifest, 657 syft".
func (s Summary) String() string {
	var sources []string
	for _, source := range slices.Sorted(maps.Keys(s.BySource)) {
		sources = append(sources, fmt.Sprintf("%d %s", s.BySource[source], source))
	}
	line := fmt.Sprintf("%d components", s.Components)
	if len(sources) > 0 {
		line += ": " + strings.Join(sources, ", ")
	}
	return line
}

func summarize(data []byte) (Summary, error) {
	var doc cycloneDX
	if err := json.Unmarshal(data, &doc); err != nil {
		return Summary{}, fmt.Errorf("%s: %w", SBOMName, err)
	}
	s := Summary{Components: len(doc.Components), BySource: map[string]int{}}
	for _, c := range doc.Components {
		for _, p := range c.Properties {
			if p.Name == sourceProperty {
				s.BySource[p.Value]++
			}
		}
	}
	return s, nil
}

// CycloneDXPackages lists the components of a CycloneDX document.
func CycloneDXPackages(data []byte) ([]Package, error) {
	var doc cycloneDX
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("the CycloneDX document does not parse: %w", err)
	}
	packages := make([]Package, 0, len(doc.Components))
	for _, c := range doc.Components {
		packages = append(packages, Package{Name: c.Name, Version: c.Version, PURL: c.PURL})
	}
	return packages, nil
}

// spdx is the part of an SPDX 2 document that the check reads. The
// package URL is one of a package's external references.
type spdx struct {
	Packages []struct {
		Name         string `json:"name"`
		VersionInfo  string `json:"versionInfo"`
		ExternalRefs []struct {
			ReferenceType    string `json:"referenceType"`
			ReferenceLocator string `json:"referenceLocator"`
		} `json:"externalRefs"`
	} `json:"packages"`
}

// SPDXPackages lists the packages of an SPDX document.
func SPDXPackages(data []byte) ([]Package, error) {
	var doc spdx
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("the SPDX document does not parse: %w", err)
	}
	packages := make([]Package, 0, len(doc.Packages))
	for _, p := range doc.Packages {
		pkg := Package{Name: p.Name, Version: p.VersionInfo}
		for _, ref := range p.ExternalRefs {
			if ref.ReferenceType == "purl" {
				pkg.PURL = ref.ReferenceLocator
				break
			}
		}
		packages = append(packages, pkg)
	}
	return packages, nil
}
