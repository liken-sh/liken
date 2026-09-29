package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A Finding is one use of another component that package.toml does
// not declare.
type Finding struct {
	Component string
	Uses      string
	Where     string
}

func (f Finding) String() string {
	if f.Uses == "" {
		return fmt.Sprintf("%s: %s", f.Component, f.Where)
	}
	return fmt.Sprintf("%s uses %s, and its package.toml does not name it in [depends]: %s", f.Component, f.Uses, f.Where)
}

// DepsChecker compares each component's [depends] with what the tools
// report: the Go modules each go.mod resolves inside the repository,
// the path crates each Cargo workspace uses, and the images and build
// contexts each Dockerfile starts from.
type DepsChecker struct {
	Root       string
	Components map[string]*Component
	// GoList and CargoMetadata run the tools. Tests replace them with
	// fixtures, because the real tools download modules and crates.
	GoList        func(dir string) ([]goModule, error)
	CargoMetadata func(dir string) ([]cargoPackage, error)
}

// Check lists every undeclared use.
func (d DepsChecker) Check() ([]Finding, error) {
	var findings []Finding
	for _, c := range sortedComponents(d.Components) {
		for _, check := range []func(*Component) ([]Finding, error){d.goModules, d.cargoWorkspaces, d.images} {
			found, err := check(c)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", c.Name(), err)
			}
			findings = append(findings, found...)
		}
	}
	return findings, nil
}

// owner names the component whose directory holds the path, relative
// to the repository root, or "" for a path that no component holds.
// A component inside another one, such as liken/kernel inside liken,
// owns its own directory.
func (d DepsChecker) owner(rel string) string { return ownerOf(d.Components, rel) }

func ownerOf(components map[string]*Component, rel string) string {
	best := ""
	for _, c := range components {
		if (rel == c.Dir || strings.HasPrefix(rel, c.Dir+"/")) && len(c.Dir) > len(best) {
			best = c.Dir
		}
	}
	for _, c := range components {
		if c.Dir == best {
			return c.Name()
		}
	}
	return ""
}

// allowed reports whether component c may use component other. A
// manual builds with brand's theme and tools, so a docs module may use
// brand without a declaration.
func (d DepsChecker) allowed(c *Component, other string, inDocs bool) bool {
	return other == "" || other == c.Name() || slices.Contains(c.Depends.Components, other) ||
		inDocs && other == "brand"
}

// walkFiles lists the files with the name under the component's
// directory, and skips the directories that hold no source, and the
// directories of components nested inside this one.
func (d DepsChecker) walkFiles(c *Component, name string) ([]string, error) {
	var files []string
	base := filepath.Join(d.Root, c.Dir)
	err := filepath.WalkDir(base, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if skipDirs[e.Name()] {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(d.Root, path)
			if path != base && d.owner(filepath.ToSlash(rel)) != c.Name() {
				return filepath.SkipDir
			}
			return nil
		}
		if e.Name() == name {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

type goModule struct {
	Path string
	Dir  string
}

// GoList runs go list over every package of the module in dir,
// together with its tests, and reports each module those packages
// come from.
func GoList(dir string) ([]goModule, error) {
	cmd := exec.Command("go", "list", "-deps", "-test", "-f", "{{with .Module}}{{.Path}}\t{{.Dir}}{{end}}", "./...")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list in %s: %w", dir, err)
	}
	seen := map[string]bool{}
	var modules []goModule
	scanner := bufio.NewScanner(strings.NewReader(string(out)))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || seen[line] {
			continue
		}
		seen[line] = true
		path, moduleDir, _ := strings.Cut(line, "\t")
		modules = append(modules, goModule{Path: path, Dir: moduleDir})
	}
	return modules, nil
}

// goModules checks each go.mod in the component. A module under
// github.com/liken-sh that Go resolves outside the repository is a
// finding too: it means a go.mod lost its replace directive and reads
// an old copy from the module proxy.
func (d DepsChecker) goModules(c *Component) ([]Finding, error) {
	gomods, err := d.walkFiles(c, "go.mod")
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(d.Root)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, gomod := range gomods {
		dir := filepath.Dir(gomod)
		rel, _ := filepath.Rel(d.Root, dir)
		inDocs := filepath.Base(dir) == "docs"
		modules, err := d.GoList(dir)
		if err != nil {
			return nil, err
		}
		for _, m := range modules {
			moduleRel, err := filepath.Rel(root, m.Dir)
			inside := err == nil && !strings.HasPrefix(moduleRel, "..")
			if !inside {
				if strings.HasPrefix(m.Path, "github.com/liken-sh/") {
					findings = append(findings, Finding{Component: c.Name(),
						Where: fmt.Sprintf("%s reads %s from the module proxy, not from the tree; add a replace directive", filepath.ToSlash(rel)+"/go.mod", m.Path)})
				}
				continue
			}
			other := d.owner(filepath.ToSlash(moduleRel))
			if !d.allowed(c, other, inDocs) {
				findings = append(findings, Finding{Component: c.Name(), Uses: other,
					Where: fmt.Sprintf("%s imports %s", filepath.ToSlash(rel)+"/go.mod", m.Path)})
			}
		}
	}
	return findings, nil
}

type cargoPackage struct {
	Name         string `json:"name"`
	ManifestPath string `json:"manifest_path"`
	Dependencies []struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Path   string `json:"path"`
	} `json:"dependencies"`
}

// CargoMetadata reads the packages of the Cargo workspace in dir and
// the dependencies each one declares.
func CargoMetadata(dir string) ([]cargoPackage, error) {
	cmd := exec.Command("cargo", "metadata", "--format-version", "1", "--no-deps")
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cargo metadata in %s: %w", dir, err)
	}
	var metadata struct {
		Packages []cargoPackage `json:"packages"`
	}
	if err := json.Unmarshal(out, &metadata); err != nil {
		return nil, err
	}
	return metadata.Packages, nil
}

// cargoWorkspaces checks each Cargo workspace in the component. A
// workspace is a directory with a Cargo.lock. A git dependency on a
// repository of the project is a finding too: the crate is in the
// tree, and a path dependency follows it.
func (d DepsChecker) cargoWorkspaces(c *Component) ([]Finding, error) {
	locks, err := d.walkFiles(c, "Cargo.lock")
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(d.Root)
	if err != nil {
		return nil, err
	}
	var findings []Finding
	for _, lock := range locks {
		dir := filepath.Dir(lock)
		packages, err := d.CargoMetadata(dir)
		if err != nil {
			return nil, err
		}
		for _, p := range packages {
			manifest, _ := filepath.Rel(root, p.ManifestPath)
			for _, dep := range p.Dependencies {
				if strings.HasPrefix(dep.Source, "git+https://github.com/liken-sh/") {
					findings = append(findings, Finding{Component: c.Name(),
						Where: fmt.Sprintf("%s takes %s from git (%s); the crate is in the tree, so use a path dependency", manifest, dep.Name, dep.Source)})
					continue
				}
				if dep.Path == "" {
					continue
				}
				rel, err := filepath.Rel(root, dep.Path)
				if err != nil || strings.HasPrefix(rel, "..") {
					continue
				}
				if other := d.owner(filepath.ToSlash(rel)); !d.allowed(c, other, false) {
					findings = append(findings, Finding{Component: c.Name(), Uses: other,
						Where: fmt.Sprintf("%s depends on the crate %s", manifest, dep.Name)})
				}
			}
		}
	}
	return findings, nil
}

// imageRef matches an image of the project in a FROM line or in an
// ARG default that a FROM line names.
var imageRef = regexp.MustCompile(`ghcr\.io/liken-sh/([a-z0-9-]+)`)

// images checks each image's build contexts, and the images of other
// components that its Dockerfile starts from: a published image by its
// ghcr.io reference, or an image in the tree by its bare name, such as
// FROM mpv, which the bake file resolves to the mpv target.
func (d DepsChecker) images(c *Component) ([]Finding, error) {
	producer := map[string]string{}
	for _, other := range d.Components {
		for _, name := range other.Packages() {
			producer[name] = other.Name()
		}
	}
	targets := ImageProducers(d.Components)
	var findings []Finding
	for _, image := range c.Outputs.Images {
		for name, dir := range image.Contexts {
			if other := d.owner(dir); !d.allowed(c, other, false) {
				findings = append(findings, Finding{Component: c.Name(), Uses: other,
					Where: fmt.Sprintf("the image %s takes %s as its build context %s", image.Name, dir, name)})
			}
		}
		file := filepath.Join(d.Root, c.Dir, orDefault(image.File, "Dockerfile"))
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		for _, base := range parseDockerfile(string(data)).Bases(image, targets) {
			if other := targets[base]; !d.allowed(c, other, false) {
				findings = append(findings, Finding{Component: c.Name(), Uses: other,
					Where: fmt.Sprintf("%s builds on the image %s", filepath.Join(c.Dir, orDefault(image.File, "Dockerfile")), base)})
			}
		}
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || (fields[0] != "FROM" && fields[0] != "ARG") {
				continue
			}
			for _, match := range imageRef.FindAllStringSubmatch(line, -1) {
				if other, ok := producer[match[1]]; ok && !d.allowed(c, other, false) {
					findings = append(findings, Finding{Component: c.Name(), Uses: other,
						Where: fmt.Sprintf("%s starts from %s", filepath.Join(c.Dir, orDefault(image.File, "Dockerfile")), match[0])})
				}
			}
		}
	}
	// Two images that share a Dockerfile, such as two targets of one
	// file, report its uses once.
	var once []Finding
	for _, f := range findings {
		if !slices.Contains(once, f) {
			once = append(once, f)
		}
	}
	return once, nil
}
