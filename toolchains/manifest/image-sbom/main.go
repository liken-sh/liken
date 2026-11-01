// Command image-sbom writes the CycloneDX record of the software in an
// image that syft cannot find on its own.
//
// buildx writes each image's SBOM with syft, and syft lists what a
// program or a package database records about itself: the build info
// of a Go binary, the crate graph that cargo-auditable writes, and the
// dpkg records of a closure. A file that a Dockerfile fetches by its
// checksum, such as a release tarball that the build compiles or a
// runtime archive that it unpacks, records nothing, and neither does a
// program that buildroot builds. syft also reads each CycloneDX file
// in an image whose name ends in .cdx.json, and copies its components
// into the image's SBOM. So an image build runs this command, and the
// final stage carries its output at /usr/share/doc/liken/<image>.cdx.json.
//
// The record comes from what the repository already holds about the
// files:
//
//   - each pin.toml that the command names, through the same reader
//     as the SBOM of an OS release, with each entry's purl, CPE,
//     license, digest, and download URL. An entry with the scope
//     excluded is a file that only the build reads, and syft ignores
//     the scope, so the record leaves the entry out;
//   - buildroot's SBOM of the build, when the image carries programs
//     that buildroot built. The record keeps the target packages, which
//     the image holds, and the graph between them. The host packages
//     ran during the build, and the virtual packages have no version.
//
// syft drops a component of the type file or data, so the command
// refuses a manifest entry of either type, and the build stops before
// it ships an image whose SBOM leaves the entry out.
//
//	go run ./image-sbom -out <file> [-buildroot <buildroot.cdx.json>] [<pin.toml>...]
package main

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/liken-sh/liken/toolchains/manifest"
)

// specVersion is the CycloneDX version of the record. buildroot's
// generate-cyclonedx writes the same version, and the syft that
// buildx's scanner runs reads it.
const specVersion = "1.6"

// droppedTypes are the CycloneDX types whose components syft leaves
// out of an image's SBOM.
var droppedTypes = []string{"file", "data"}

type document struct {
	BOMFormat    string           `json:"bomFormat"`
	SpecVersion  string           `json:"specVersion"`
	SerialNumber string           `json:"serialNumber,omitempty"`
	Version      int              `json:"version"`
	Components   []map[string]any `json:"components"`
	Dependencies []dependency     `json:"dependencies,omitempty"`
}

type dependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// buildrootSBOM is the part of buildroot's SBOM that the command
// reads. buildroot's vulnerabilities are left out on purpose: they are
// the result of buildroot's check on the day of the build, and a
// scanner that reads the image matches the advisories of the day it
// runs.
type buildrootSBOM struct {
	Components   []map[string]any `json:"components"`
	Dependencies []dependency     `json:"dependencies"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "image-sbom:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("image-sbom", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	out := fs.String("out", "", "the file to write")
	buildroot := fs.String("buildroot", "", "buildroot's CycloneDX SBOM of the build")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *out == "":
		return errors.New("-out is required")
	case fs.NArg() == 0 && *buildroot == "":
		return errors.New("name a pin.toml or -buildroot, the sources of the record")
	}
	doc := document{BOMFormat: "CycloneDX", SpecVersion: specVersion, Version: 1}
	for _, path := range fs.Args() {
		components, err := shipped(path)
		if err != nil {
			return err
		}
		doc.Components = append(doc.Components, components...)
	}
	if *buildroot != "" {
		components, graph, err := targetPackages(*buildroot)
		if err != nil {
			return err
		}
		doc.Components = append(doc.Components, components...)
		doc.Dependencies = graph
	}
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	// The serial number is a digest of the rest of the record, so two
	// builds of one tree write the same bytes, and the layer that holds
	// the record stays cached.
	sum := sha256.Sum256(data)
	doc.SerialNumber = fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	data, err = json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(*out, append(data, '\n'), 0o644)
}

// shipped reads the components of one manifest that the image ships.
func shipped(path string) ([]map[string]any, error) {
	all, err := manifest.Components(path)
	if err != nil {
		return nil, err
	}
	var kept []map[string]any
	for _, c := range all {
		if c["scope"] == "excluded" {
			continue
		}
		if kind := c["type"].(string); slices.Contains(droppedTypes, kind) {
			return nil, fmt.Errorf("%s: [%s] has the type %s, which syft drops from an image's SBOM; give the entry a type such as library or application", path, c["name"], kind)
		}
		kept = append(kept, c)
	}
	return kept, nil
}

// targetPackages reads buildroot's SBOM and keeps each target package
// that has a version, with its references prefixed by buildroot:, and
// the edges of buildroot's graph between the packages that it keeps.
func targetPackages(path string) ([]map[string]any, []dependency, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var in buildrootSBOM
	if err := json.Unmarshal(data, &in); err != nil {
		return nil, nil, fmt.Errorf("reading %s: %w", path, err)
	}
	prefix := func(ref string) string { return "buildroot:" + ref }
	kept := map[string]bool{}
	var components []map[string]any
	for _, c := range in.Components {
		version, _ := c["version"].(string)
		if property(c, "BR_TYPE") != "target" || c["scope"] == "excluded" || version == "" {
			continue
		}
		ref, _ := c["bom-ref"].(string)
		kept[ref] = true
		c["bom-ref"] = prefix(ref)
		components = append(components, c)
	}
	var graph []dependency
	for _, d := range in.Dependencies {
		if !kept[d.Ref] {
			continue
		}
		edge := dependency{Ref: prefix(d.Ref)}
		for _, on := range d.DependsOn {
			if kept[on] {
				edge.DependsOn = append(edge.DependsOn, prefix(on))
			}
		}
		graph = append(graph, edge)
	}
	return components, graph, nil
}

// property reads one property of a component.
func property(c map[string]any, name string) string {
	list, _ := c["properties"].([]any)
	for _, p := range list {
		if p, ok := p.(map[string]any); ok && p["name"] == name {
			value, _ := p["value"].(string)
			return value
		}
	}
	return ""
}
