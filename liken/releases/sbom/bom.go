package main

// The parts of a CycloneDX document that the merge reads and writes.
//
// A component is a map, not a struct, because the merge copies the
// components of buildroot and syft with every field they hold, and
// changes only the bom-ref, the properties, and the reference names in
// the dependency graph. A struct would drop each field that it does not
// declare.

// bom is the document that the merge writes.
type bom struct {
	Schema       string           `json:"$schema"`
	BOMFormat    string           `json:"bomFormat"`
	SpecVersion  string           `json:"specVersion"`
	SerialNumber string           `json:"serialNumber"`
	Version      int              `json:"version"`
	Metadata     metadata         `json:"metadata"`
	Components   []map[string]any `json:"components"`
	Dependencies []dependency     `json:"dependencies"`
	Compositions []composition    `json:"compositions"`
}

type metadata struct {
	Tools     tools          `json:"tools"`
	Component map[string]any `json:"component"`
}

type tools struct {
	Components []map[string]any `json:"components"`
}

// input is the part of buildroot's SBOM and of syft's SBOM that the
// merge reads. buildroot's vulnerabilities are left out on purpose:
// they are the result of buildroot's check on the day of the build,
// and a scanner that reads this SBOM matches the advisories of the
// day it runs.
type input struct {
	Metadata     metadata         `json:"metadata"`
	Components   []map[string]any `json:"components"`
	Dependencies []dependency     `json:"dependencies"`
}

type dependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn,omitempty"`
}

// A composition states whether the SBOM lists all of something. The
// aggregate "complete" says that the dependencies of each component
// that the composition names are all listed.
type composition struct {
	Aggregate    string   `json:"aggregate"`
	Dependencies []string `json:"dependencies"`
}

type hash struct {
	Alg     string `json:"alg"`
	Content string `json:"content"`
}

type license struct {
	Expression string `json:"expression"`
}

type reference struct {
	Type   string `json:"type"`
	URL    string `json:"url"`
	Hashes []hash `json:"hashes,omitempty"`
}
