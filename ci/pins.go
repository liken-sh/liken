package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// checkPins lists every pin that two files of the repository share and
// name at different versions. Each copy of a pin can drift on its own,
// and a drift builds or runs two versions of one thing, so the check
// fails at the commit that makes the copies differ:
//
//   - each go.mod names the Go of the golang image in images.toml, and
//     each rust-toolchain.toml names the Rust of the rust image;
//   - each Go module that a go.mod requires directly, or as a tool, has
//     one version across every go.mod, and each crate has one version
//     across every Cargo.toml;
//   - each hook repository has one revision across every
//     .pre-commit-config.yaml;
//   - each image of images.toml that a manifest, a guide, a skill, a
//     Makefile, or a test names has the tag of its entry;
//   - each copy of a Wayland protocol that liken defines, such as
//     liken-arrangement-v1.xml, holds the same bytes. The compositor
//     module and each client build from their own directory, so each
//     keeps a copy, and a client built from a drifted copy sends
//     requests that the compositor does not read the same way.
//
// The plans keep the versions of their day, so the check reads none of
// them. Neither does it read test data, a cache, or another checkout.
func checkPins(root string) ([]string, error) {
	tools, err := readToolchains(root)
	if err != nil {
		return nil, err
	}
	files, err := pinFiles(root)
	if err != nil {
		return nil, err
	}
	goVersion := strings.SplitN(parseImageRef(tools.Build["golang"]).Tag, "-", 2)[0]
	rustVersion := strings.SplitN(parseImageRef(tools.Build["rust"]).Tag, "-", 2)[0]
	modules, crates, hooks, protocols := versions{}, versions{}, versions{}, versions{}
	var findings []string
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(root, file))
		if errors.Is(err, fs.ErrNotExist) {
			// git still tracks a file that the tree deleted.
			continue
		}
		if err != nil {
			return nil, err
		}
		var found []string
		switch name := path.Base(file); {
		case name == "go.mod":
			found = readGoMod(file, string(data), goVersion, modules)
		case name == "rust-toolchain.toml":
			found, err = readRustToolchain(file, data, rustVersion)
		case name == "Cargo.toml":
			err = readCargo(file, data, crates)
		case name == ".pre-commit-config.yaml":
			err = readHooks(file, data, hooks)
		case isProtocol(name):
			protocols.add(name, contentDigest(data), file)
		}
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
		if namesImages(file) {
			findings = append(findings, imageDrift(file, string(data), tools)...)
		}
	}
	findings = append(findings, modules.drift("the Go module")...)
	findings = append(findings, crates.drift("the crate")...)
	findings = append(findings, hooks.drift("the hook repository")...)
	findings = append(findings, protocols.drift("the Wayland protocol")...)
	return findings, nil
}

// pinFiles lists the files of the repository that can hold a pin: the
// files that git tracks or would track, so a download, a build output,
// or another checkout that .gitignore leaves out is not read. Test data
// keeps the versions that its test needs, and a plan keeps the
// versions of its day.
func pinFiles(root string) ([]string, error) {
	out, err := Git{Dir: root}.run("ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, file := range strings.Split(out, "\x00") {
		dirs := strings.Split(path.Dir(file), "/")
		if file == "" || slices.Contains(dirs, "testdata") || slices.Contains(dirs, "plans") || slices.Contains(dirs, ".cache") {
			continue
		}
		switch name := path.Base(file); {
		case name == "go.mod", name == "rust-toolchain.toml", name == "Cargo.toml", name == ".pre-commit-config.yaml", isProtocol(name), namesImages(file):
			files = append(files, file)
		}
	}
	slices.Sort(files)
	return slices.Compact(files), nil
}

// isProtocol is true for the file of a Wayland protocol that liken
// defines: liken-<name>-v<N>.xml, the form wayland-scanner reads.
func isProtocol(name string) bool {
	stem, ok := strings.CutSuffix(name, ".xml")
	if !ok || !strings.HasPrefix(stem, "liken-") {
		return false
	}
	i := strings.LastIndex(stem, "-v")
	return i > 0 && i+2 < len(stem) && strings.Trim(stem[i+2:], "0123456789") == ""
}

// contentDigest names a file's bytes in a finding: the first twelve
// hex digits of their SHA-256, enough to tell two copies apart.
func contentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])[:12]
}

// namesImages is true for a file whose image references the check
// reads. ci's own tests name drifted images on purpose, as fixtures of
// this check and of the Dockerfile parser. flux generates
// gotk-components.yaml, and its images move with liken/flux/VERSION. A
// file of license notices quotes how third-party software was built,
// which is not an image that liken runs.
func namesImages(file string) bool {
	name := path.Base(file)
	switch {
	case name == "gotk-components.yaml", name == "NOTICES.md", name == "LICENSES.md",
		strings.HasPrefix(file, "ci/") && strings.HasSuffix(name, "_test.go"):
		return false
	case strings.HasPrefix(file, ".agents/skills/"), strings.Contains("/"+file, "/skills/"):
		return true
	}
	return name == "Makefile" || strings.HasSuffix(name, "_test.go") ||
		slices.Contains([]string{".yaml", ".yml", ".md", ".rs"}, path.Ext(name))
}

// versions maps each pinned thing to each version it is named at, and
// each version to the files that name it.
type versions map[string]map[string][]string

func (v versions) add(name, version, file string) {
	if v[name] == nil {
		v[name] = map[string][]string{}
	}
	v[name][version] = appendOnce(v[name][version], file)
}

// drift lists each name with more than one version.
func (v versions) drift(kind string) []string {
	var findings []string
	for _, name := range slices.Sorted(maps.Keys(v)) {
		if len(v[name]) < 2 {
			continue
		}
		var parts []string
		for _, version := range slices.Sorted(maps.Keys(v[name])) {
			parts = append(parts, version+" in "+strings.Join(v[name][version], ", "))
		}
		findings = append(findings, fmt.Sprintf("%s %s has %d versions: %s", kind, name, len(v[name]), strings.Join(parts, "; ")))
	}
	return findings
}

// readGoMod adds each module that the go.mod requires directly, or as a
// tool, to modules, and checks its go directive. A module that a
// replace directive points at a directory is a module of this
// repository, and its version means nothing. An indirect requirement
// is what the module's own requirements choose, so it follows them.
func readGoMod(file, text, goVersion string, modules versions) []string {
	var findings []string
	required := map[string]string{}
	var direct, tools, local []string
	block := ""
	for _, line := range strings.Split(text, "\n") {
		code, comment, _ := strings.Cut(line, "//")
		fields := strings.Fields(code)
		verb := block
		switch {
		case len(fields) == 0:
			continue
		case fields[0] == ")":
			block = ""
			continue
		case len(fields) == 2 && fields[1] == "(":
			block = fields[0]
			continue
		case block == "":
			verb, fields = fields[0], fields[1:]
		}
		switch verb {
		case "go":
			if len(fields) == 1 && fields[0] != goVersion {
				findings = append(findings, fmt.Sprintf("%s names go %s, and the golang image in %s is %s", file, fields[0], imagesFile, goVersion))
			}
		case "require":
			if len(fields) == 2 {
				required[fields[0]] = fields[1]
				if strings.TrimSpace(comment) != "indirect" {
					direct = append(direct, fields[0])
				}
			}
		case "tool":
			tools = append(tools, fields...)
		case "replace":
			if i := slices.Index(fields, "=>"); i >= 0 && i+1 < len(fields) && strings.HasPrefix(fields[i+1], ".") {
				local = append(local, fields[0])
			}
		}
	}
	for module, version := range required {
		isTool := slices.ContainsFunc(tools, func(tool string) bool { return within(tool, module) })
		if (slices.Contains(direct, module) || isTool) && !slices.Contains(local, module) {
			modules.add(module, version, file)
		}
	}
	return findings
}

// readRustToolchain checks the channel of a rust-toolchain.toml.
func readRustToolchain(file string, data []byte, rustVersion string) ([]string, error) {
	var config struct {
		Toolchain struct {
			Channel string `toml:"channel"`
		} `toml:"toolchain"`
	}
	if _, err := toml.Decode(string(data), &config); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	if channel := config.Toolchain.Channel; channel != rustVersion {
		return []string{fmt.Sprintf("%s names Rust %s, and the rust image in %s is %s", file, channel, imagesFile, rustVersion)}, nil
	}
	return nil, nil
}

// readCargo adds each crate that a Cargo.toml names with a version to
// crates: in the dependency tables of a package, of a target, and of a
// workspace. A crate that takes its version from the workspace names
// none of its own.
func readCargo(file string, data []byte, crates versions) error {
	var manifest map[string]any
	if _, err := toml.Decode(string(data), &manifest); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	tables := []any{manifest}
	if workspace, ok := manifest["workspace"].(map[string]any); ok {
		tables = append(tables, workspace)
	}
	if targets, ok := manifest["target"].(map[string]any); ok {
		for _, target := range targets {
			tables = append(tables, target)
		}
	}
	for _, table := range tables {
		table, _ := table.(map[string]any)
		for _, kind := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
			deps, _ := table[kind].(map[string]any)
			for crate, spec := range deps {
				version, _ := spec.(string)
				if detail, ok := spec.(map[string]any); ok {
					version, _ = detail["version"].(string)
				}
				if version != "" {
					crates.add(crate, version, file)
				}
			}
		}
	}
	return nil
}

// readHooks adds the revision of each hook repository to hooks. The
// local and meta repositories have no revision.
func readHooks(file string, data []byte, hooks versions) error {
	var config struct {
		Repos []struct {
			Repo string `yaml:"repo"`
			Rev  string `yaml:"rev"`
		} `yaml:"repos"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("%s: %w", file, err)
	}
	for _, repo := range config.Repos {
		if repo.Rev != "" {
			hooks.add(repo.Repo, repo.Rev, file)
		}
	}
	return nil
}
