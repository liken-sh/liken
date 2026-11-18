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
	"regexp"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
	"gopkg.in/yaml.v3"
)

// checkPins returns a finding for each pin whose copies in the
// repository disagree. A pin copied into several files drifts when one
// copy changes alone, and the repository then builds two versions of
// one thing, so the check fails at the commit that changes one copy.
// It compares:
//
//   - the go directive of each go.mod with the tag of the golang image
//     in images.toml, and the channel of each rust-toolchain.toml with
//     the tag of the rust image.
//   - the version of each Go module and each crate across all go.mod
//     and Cargo.toml files, and the rev of each hook repository across
//     all .pre-commit-config.yaml files.
//   - the tag of each image of images.toml with every reference to that
//     image in a manifest, a guide, a skill, a Makefile, or a test.
//   - the bytes of each copy of a Wayland protocol that liken defines,
//     such as liken-arrangement-v1.xml. The compositor and each client
//     build from their own directory, so each keeps a copy, and a
//     client built from a different copy sends requests that the
//     compositor decodes differently.
//   - the bytes of each copy of closure.sh. indi keeps a copy of
//     vulkan/closure.sh because its build context does not include
//     vulkan/.
//   - the BUILDROOT_COMMIT and BUILDROOT_SHA256 of each Dockerfile with
//     liken/userland/VERSION and its digest in liken/userland/fetch.sh,
//     because the image and the OS build from one BR2_EXTERNAL tree,
//     written for one buildroot commit.
//   - the SPDX_SCHEMA_SHA256 of each Dockerfile with the
//     spdx_schema_sha256 table of liken/userland/fetch.sh, so the
//     image's SBOM and the OS's SBOM use the same SPDX license list.
//
// It also fails on a Dockerfile that names an outside image by its full
// reference, and on a shell script that runs an image by digest,
// because each is a copy of a pin that images.toml does not hold. The
// Dockerfile rules of unauditedBuilds, labelInstructions, and
// goImageFindings run here too.
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
	modules, crates, hooks, protocols, closures := versions{}, versions{}, versions{}, versions{}, versions{}
	buildroots, err := userlandBuildroot(root)
	if err != nil {
		return nil, err
	}
	schemas, err := userlandSchemas(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for _, file := range files {
		data, err := os.ReadFile(filepath.Join(root, file))
		if errors.Is(err, fs.ErrNotExist) {
			// git can list a file that is deleted from the working tree.
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
		case name == closureLibrary:
			closures.add(name, contentDigest(data), file)
		}
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
		if namesImages(file) {
			findings = append(findings, imageDrift(file, string(data), tools)...)
		}
		if isScript(path.Base(file)) {
			findings = append(findings, scriptImages(file, string(data))...)
		}
		if isDockerfile(path.Base(file)) {
			findings = append(findings, outsideImages(file, string(data))...)
			findings = append(findings, unauditedBuilds(file, string(data))...)
			findings = append(findings, labelInstructions(file, string(data))...)
			findings = append(findings, goImageFindings(file, string(data))...)
			if pin := dockerfileBuildroot(string(data)); pin != "" {
				buildroots.add("buildroot", pin, file)
			}
			findings = append(findings, schemaFindings(file, string(data), schemas)...)
		}
	}
	findings = append(findings, modules.drift("the Go module")...)
	findings = append(findings, crates.drift("the crate")...)
	findings = append(findings, hooks.drift("the hook repository")...)
	findings = append(findings, protocols.drift("the Wayland protocol")...)
	findings = append(findings, closures.drift("the closure library")...)
	findings = append(findings, buildroots.drift("the buildroot pin")...)
	return findings, nil
}

// userlandBuildroot returns the buildroot pin of liken/userland as
// "<commit> sha256:<digest>": the commit in VERSION and its digest in
// fetch.sh. A tree without liken/userland/VERSION returns no pin.
func userlandBuildroot(root string) (versions, error) {
	pins := versions{}
	data, err := os.ReadFile(filepath.Join(root, "liken/userland/VERSION"))
	if errors.Is(err, fs.ErrNotExist) {
		return pins, nil
	}
	if err != nil {
		return nil, err
	}
	commit := strings.TrimSpace(string(data))
	fetch, err := os.ReadFile(filepath.Join(root, "liken/userland/fetch.sh"))
	if err != nil {
		return nil, err
	}
	digest := ""
	prefix := `["` + commit + `"]="`
	for _, line := range strings.Split(string(fetch), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix); ok {
			digest = strings.TrimSuffix(rest, `"`)
		}
	}
	pins.add("buildroot", commit+" sha256:"+digest, "liken/userland/VERSION")
	return pins, nil
}

// userlandSchemas returns the spdx_schema_sha256 table of
// liken/userland/fetch.sh, which maps each version of the SPDX license
// list to its digest.
func userlandSchemas(root string) (map[string]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "liken/userland/fetch.sh"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	schemas := map[string]string{}
	inside := false
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "declare -A spdx_schema_sha256=(":
			inside = true
		case inside && line == ")":
			inside = false
		case inside:
			if m := tableEntry.FindStringSubmatch(line); m != nil {
				schemas[m[1]] = m[2]
			}
		}
	}
	return schemas, nil
}

// tableEntry matches one entry of a bash associative array:
// ["key"]="value".
var tableEntry = regexp.MustCompile(`^\["([^"]+)"\]="([^"]*)"$`)

// schemaFindings returns a finding when a Dockerfile's
// SPDX_SCHEMA_SHA256 differs from the digest that schemas has for its
// SPDX_SCHEMA_VERSION.
func schemaFindings(file, text string, schemas map[string]string) []string {
	args := dockerfileArgs(text)
	version, digest := args["SPDX_SCHEMA_VERSION"], args["SPDX_SCHEMA_SHA256"]
	if version == "" && digest == "" {
		return nil
	}
	if pinned := schemas[version]; pinned != digest {
		return []string{fmt.Sprintf("%s fetches the SPDX schema %s by sha256:%s, and the spdx_schema_sha256 table of liken/userland/fetch.sh pins sha256:%s", file, version, digest, pinned)}
	}
	return nil
}

// dockerfileArgs reads the default of each ARG of a Dockerfile.
func dockerfileArgs(text string) map[string]string {
	args := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "ARG "); ok {
			if name, value, found := strings.Cut(rest, "="); found {
				args[name] = value
			}
		}
	}
	return args
}

// dockerfileBuildroot returns the BUILDROOT_COMMIT and
// BUILDROOT_SHA256 arguments of a Dockerfile in the form that
// userlandBuildroot returns, or "" when the Dockerfile has neither.
func dockerfileBuildroot(text string) string {
	args := dockerfileArgs(text)
	if args["BUILDROOT_COMMIT"] == "" && args["BUILDROOT_SHA256"] == "" {
		return ""
	}
	return args["BUILDROOT_COMMIT"] + " sha256:" + args["BUILDROOT_SHA256"]
}

// pinFiles returns the files of sourceFiles that can hold a pin.
func pinFiles(root string) ([]string, error) {
	all, err := sourceFiles(root)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, file := range all {
		switch name := path.Base(file); {
		case name == "go.mod", name == "rust-toolchain.toml", name == "Cargo.toml", name == ".pre-commit-config.yaml", name == closureLibrary, isProtocol(name), isDockerfile(name), namesImages(file), isScript(name):
			files = append(files, file)
		}
	}
	return files, nil
}

// sourceFiles returns the sorted paths of the files that git tracks,
// and of the untracked files that .gitignore does not exclude. It
// leaves out files under testdata, plans, and .cache directories,
// because test data keeps the versions its test needs and a plan keeps
// the versions of its day. A tracked file that is deleted from the
// working tree stays in the list.
func sourceFiles(root string) ([]string, error) {
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
		files = append(files, file)
	}
	slices.Sort(files)
	return slices.Compact(files), nil
}

const closureLibrary = "closure.sh"

// isProtocol is true for liken-<name>-v<N>.xml, the file name of a
// Wayland protocol that liken defines.
func isProtocol(name string) bool {
	stem, ok := strings.CutSuffix(name, ".xml")
	if !ok || !strings.HasPrefix(stem, "liken-") {
		return false
	}
	i := strings.LastIndex(stem, "-v")
	return i > 0 && i+2 < len(stem) && strings.Trim(stem[i+2:], "0123456789") == ""
}

// contentDigest returns the first twelve hex digits of the SHA-256 of
// data, to show in a finding.
func contentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])[:12]
}

// namesImages is true for a file whose image references checkPins
// compares with images.toml. It is false for the tests of ci, which
// hold drifted references on purpose as fixtures, for
// gotk-components.yaml, whose images flux generates from
// liken/flux/VERSION, and for NOTICES.md and LICENSES.md, which quote
// the images that third-party software was built with.
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

// versions maps the name of a pin to each version found, and each
// version to the files that have it.
type versions map[string]map[string][]string

func (v versions) add(name, version, file string) {
	if v[name] == nil {
		v[name] = map[string][]string{}
	}
	v[name][version] = appendOnce(v[name][version], file)
}

// drift returns a finding for each name that has more than one
// version. kind starts the finding, such as "the crate".
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

// readGoMod adds each module that the go.mod requires directly or as a
// tool to modules, and returns a finding when its go directive is not
// goVersion. It skips a module that a replace directive maps to a local
// directory, because the module is in this repository and its version
// is a placeholder. It skips an indirect requirement, because its
// version follows the direct requirements.
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
				findings = append(findings, fmt.Sprintf("%s sets go %s, and the golang image in %s is %s", file, fields[0], imagesFile, goVersion))
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

// readRustToolchain returns a finding when the channel of a
// rust-toolchain.toml is not rustVersion.
func readRustToolchain(file string, data []byte, rustVersion string) ([]string, error) {
	var config struct {
		Toolchain struct {
			Channel string `toml:"channel"`
		} `toml:"toolchain"`
	}
	if _, err := toml.Decode(string(data), &config); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", file, err)
	}
	if channel := config.Toolchain.Channel; channel != rustVersion {
		return []string{fmt.Sprintf("%s sets Rust %s, and the rust image in %s is %s", file, channel, imagesFile, rustVersion)}, nil
	}
	return nil, nil
}

// readCargo adds each crate that a Cargo.toml requires with a version
// to crates, from the dependency tables of the package, of each target,
// and of the workspace. A dependency with workspace = true or only a
// path has no version, so it adds nothing.
func readCargo(file string, data []byte, crates versions) error {
	var manifest map[string]any
	if _, err := toml.Decode(string(data), &manifest); err != nil {
		return fmt.Errorf("parsing %s: %w", file, err)
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

// readHooks adds the rev of each hook repository of a
// .pre-commit-config.yaml to hooks. It skips the local and meta
// repositories, which have no rev.
func readHooks(file string, data []byte, hooks versions) error {
	var config struct {
		Repos []struct {
			Repo string `yaml:"repo"`
			Rev  string `yaml:"rev"`
		} `yaml:"repos"`
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parsing %s: %w", file, err)
	}
	for _, repo := range config.Repos {
		if repo.Rev != "" {
			hooks.add(repo.Repo, repo.Rev, file)
		}
	}
	return nil
}
