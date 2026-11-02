package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liken-sh/liken/liken/api"
	"github.com/liken-sh/liken/liken/releases/sbom/schema"
)

// rootRef is the bom-ref of the release, the component that the SBOM
// describes.
const rootRef = "liken"

// sourceProperty names the source of each component: manifest,
// buildroot, or syft. A reader counts or filters the components by it.
const sourceProperty = "liken:source"

// A scan is one SBOM that syft wrote, and the component whose contents
// syft scanned: the release, for the root filesystem, an image archive,
// or the toolkit, or a pin such as pin:k3s, for the programs that k3s
// carries inside its binary.
type scan struct{ parent, file string }

// inputs are the sources of the SBOM. claims maps the path of a file
// in a syft scan to the component that already names the file, such
// as /bin/k3s to pin:k3s, so the SBOM lists the program once, under
// the manifest's purl and license.
type inputs struct {
	version   string
	manifests []string
	buildroot string
	scans     []scan
	claims    map[string]string
}

// merger collects the components, the dependency graph, and the
// statements of completeness, in the order the sources add them.
type merger struct {
	components []map[string]any
	refs       map[string]bool
	deps       map[string][]string
	complete   []string
	incomplete []string
	tools      []map[string]any
}

// merge reads every source and writes one SBOM of the release. The
// manifests go first, then buildroot, and then syft, so a claim can
// name a component of either of the first two.
func merge(in inputs) (*bom, error) {
	if err := api.ValidVersion(in.version); err != nil {
		return nil, err
	}
	m := &merger{refs: map[string]bool{rootRef: true}, deps: map[string][]string{}}
	for _, path := range in.manifests {
		components, err := manifestComponents(path)
		if err != nil {
			return nil, err
		}
		for _, c := range components {
			if err := m.add(c); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			m.dependOn(rootRef, c["bom-ref"].(string))
		}
	}
	if err := m.addBuildroot(in.buildroot); err != nil {
		return nil, err
	}
	for path, ref := range in.claims {
		if !m.refs[ref] {
			return nil, fmt.Errorf("the claim of %s names %s, which no manifest and no buildroot package is", path, ref)
		}
	}
	names := map[string]bool{}
	for _, s := range in.scans {
		name := scanName(s.file)
		if names[name] {
			return nil, fmt.Errorf("two syft scans are named %s, and each scan needs its own name for its references", name)
		}
		names[name] = true
		if !m.refs[s.parent] {
			return nil, fmt.Errorf("the scan %s is inside %s, which no source names", s.file, s.parent)
		}
		if err := m.addScan(s, name, in.claims); err != nil {
			return nil, err
		}
	}
	// The release as a whole is incomplete. syft identifies a program by
	// the record that the program carries, such as a Go binary's build
	// info, and a static C program carries none, so the libraries that
	// such a program links are listed only when its own build wrote an
	// SBOM, as buildroot does.
	m.incomplete = append(m.incomplete, rootRef)
	return m.document(in.version)
}

// add appends one component, and refuses a second component with the
// same reference.
func (m *merger) add(c map[string]any) error {
	ref, _ := c["bom-ref"].(string)
	if ref == "" || m.refs[ref] {
		return fmt.Errorf("the component reference %q is empty or taken", ref)
	}
	m.refs[ref] = true
	m.components = append(m.components, c)
	return nil
}

// dependOn records that one component depends on others, once each.
func (m *merger) dependOn(ref string, on ...string) {
	for _, o := range on {
		if o != ref && !slices.Contains(m.deps[ref], o) {
			m.deps[ref] = append(m.deps[ref], o)
		}
	}
}

// addBuildroot takes buildroot's SBOM whole: the buildroot build as a
// component that the release depends on, each package with its
// scope, and buildroot's graph between them. A host package has the
// scope excluded, because it ran during the build and the release does
// not carry it. buildroot lists every package that it built, so its
// graph is complete.
func (m *merger) addBuildroot(path string) error {
	in, err := readInput(path)
	if err != nil {
		return err
	}
	prefix := func(ref string) string { return "buildroot:" + ref }
	top := maps.Clone(in.Metadata.Component)
	ref, _ := top["bom-ref"].(string)
	for _, c := range append([]map[string]any{top}, in.Components...) {
		c["bom-ref"] = prefix(c["bom-ref"].(string))
		addProperty(c, sourceProperty, "buildroot")
		if err := m.add(c); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	for _, d := range in.Dependencies {
		for _, on := range d.DependsOn {
			m.dependOn(prefix(d.Ref), prefix(on))
		}
	}
	m.dependOn(rootRef, prefix(ref))
	m.complete = append(m.complete, prefix(ref))
	m.tools = append(m.tools, in.Metadata.Tools.Components...)
	return nil
}

// addScan takes the packages of one syft scan.
//
// syft writes a component for the operating system that it reads from
// os-release or from a program such as busybox, and a file component
// for each file that a package came from. The SBOM has neither: the
// scan's parent is the operating system, and the file is the package. A program at a claimed path
// is the component that the claim names, so its component is dropped
// and the references to it point at the claim. The modules that the
// program links stay, as dependencies of the claimed component.
//
// A package that no other package of the scan depends on is a program
// of its own, and the scan's parent depends on it. liken's own Go
// programs build with CGO_ENABLED=0, so the build info that syft reads
// lists every module they link, and their dependencies are complete.
// A scan inside a pin lists only the programs that syft identifies, so
// that pin's dependencies are incomplete.
func (m *merger) addScan(s scan, name string, claims map[string]string) error {
	in, err := readInput(s.file)
	if err != nil {
		return err
	}
	redirect := map[string]string{}
	var kept []string
	for _, c := range in.Components {
		ref, _ := c["bom-ref"].(string)
		target, claimed := claimOf(claims, property(c, "syft:location:0:path"))
		switch {
		case c["type"] == "operating-system":
			redirect[ref] = s.parent
		case c["type"] == "file":
		case claimed && isProgram(c):
			redirect[ref] = target
		default:
			redirect[ref] = "syft:" + name + ":" + ref
			c["bom-ref"] = redirect[ref]
			addProperty(c, sourceProperty, "syft")
			if err := m.add(c); err != nil {
				return fmt.Errorf("%s: %w", s.file, err)
			}
			kept = append(kept, redirect[ref])
			if isProgram(c) && strings.HasPrefix(c["name"].(string), "github.com/liken-sh/") {
				m.complete = append(m.complete, redirect[ref])
			}
		}
	}
	depended := map[string]bool{}
	for _, d := range in.Dependencies {
		from := redirect[d.Ref]
		for _, on := range d.DependsOn {
			if to := redirect[on]; from != "" && to != "" {
				m.dependOn(from, to)
				depended[to] = true
			}
		}
	}
	for _, ref := range kept {
		if !depended[ref] {
			m.dependOn(s.parent, ref)
		}
	}
	if s.parent != rootRef && !slices.Contains(m.incomplete, s.parent) {
		m.incomplete = append(m.incomplete, s.parent)
	}
	m.tools = append(m.tools, in.Metadata.Tools.Components...)
	return nil
}

// claimOf finds the claim of a path: the claim of the path itself, or
// the claim of a directory above it, which ends in a slash. syft lists
// each kernel module under /lib/modules/ as a package of its own, and
// one manifest entry names all of them.
func claimOf(claims map[string]string, path string) (string, bool) {
	if ref, ok := claims[path]; ok {
		return ref, true
	}
	for dir, ref := range claims {
		if strings.HasSuffix(dir, "/") && strings.HasPrefix(path, dir) {
			return ref, true
		}
	}
	return "", false
}

// isProgram is true for a component that stands for a whole file: the
// main module of a Go binary, or a package of another kind, such as a
// program that syft's binary classifier names. The other modules of a
// Go binary, the standard library among them, are what the program
// links, and they stay.
func isProgram(c map[string]any) bool {
	if property(c, "syft:package:type") == "go-module" {
		return property(c, "syft:metadata:mainModule") == c["name"]
	}
	return true
}

// document writes the SBOM. The serial number is a digest of the rest
// of the document, so two builds of one tree write the same bytes, the
// way the release's other files do, and two releases never share one.
func (m *merger) document(version string) (*bom, error) {
	b := &bom{
		Schema:      schema.URL,
		BOMFormat:   "CycloneDX",
		SpecVersion: schema.SpecVersion,
		Version:     1,
		Metadata: metadata{
			Tools:     tools{Components: uniqueTools(m.tools)},
			Component: releaseComponent(version),
		},
		Components: m.components,
	}
	for _, ref := range slices.Sorted(maps.Keys(m.deps)) {
		b.Dependencies = append(b.Dependencies, dependency{Ref: ref, DependsOn: slices.Sorted(slices.Values(m.deps[ref]))})
	}
	slices.Sort(m.incomplete)
	slices.Sort(m.complete)
	b.Compositions = []composition{
		{Aggregate: "incomplete", Dependencies: m.incomplete},
		{Aggregate: "complete", Dependencies: m.complete},
	}
	data, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	b.SerialNumber = fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
	return b, nil
}

// releaseComponent is the release: the OS at its CalVer version, under
// liken's own license, at its directory on the release channel.
func releaseComponent(version string) map[string]any {
	return map[string]any{
		"bom-ref":  rootRef,
		"type":     "operating-system",
		"name":     "liken",
		"version":  version,
		"licenses": []license{{Expression: "MIT"}},
		"externalReferences": []reference{
			{Type: "distribution", URL: "https://releases.liken.sh/" + version + "/"},
			{Type: "vcs", URL: "https://github.com/liken-sh/liken"},
		},
	}
}

// uniqueTools lists each tool once, because every syft scan names the
// same syft.
func uniqueTools(all []map[string]any) []map[string]any {
	seen := map[string]bool{}
	var unique []map[string]any
	for _, t := range all {
		key := fmt.Sprint(t["name"], "@", t["version"])
		if !seen[key] {
			seen[key] = true
			unique = append(unique, t)
		}
	}
	return unique
}

func readInput(path string) (input, error) {
	var in input
	data, err := os.ReadFile(path)
	if err != nil {
		return in, err
	}
	if err := json.Unmarshal(data, &in); err != nil {
		return in, fmt.Errorf("reading %s: %w", path, err)
	}
	return in, nil
}

// scanName is the name of a scan in its components' references: the
// file name without its extension, as in syft-root for
// syft-root.cdx.json.
func scanName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(strings.TrimSuffix(base, ".json"), ".cdx")
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

// addProperty appends one property to a component.
func addProperty(c map[string]any, name, value string) {
	list, _ := c["properties"].([]any)
	c["properties"] = append(list, map[string]any{"name": name, "value": value})
}
