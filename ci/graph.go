package main

import (
	"path"
	"slices"
	"strings"
)

// Closure lists the component and every component it depends on,
// directly or through another dependency, sorted by name.
func Closure(components map[string]*Component, name string) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(n string) {
		if seen[n] {
			return
		}
		seen[n] = true
		for _, dep := range components[n].Depends.Components {
			walk(dep)
		}
	}
	walk(name)
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// notOutputs are the paths at the top of a component that no output
// is built from: the manual, the plans, the agent and reader notes, and
// the smoke checks, which test an image and do not go into it. A change
// to them runs the component's own jobs, and it does not give the
// component a new version.
var notOutputs = []string{"docs/", "plans/", "AGENTS.md", "README.md", "smoke/"}

// isNotOutput is true for a path, relative to its component, that no
// output is built from: a path in notOutputs, a Go test file, or a
// file under a testdata directory at any depth. Go compiles a test
// file and reads test data only for the tests of its own package, so
// neither one goes into a binary.
func isNotOutput(rest string) bool {
	for _, prefix := range notOutputs {
		if strings.HasSuffix(prefix, "/") && strings.HasPrefix(rest, prefix) || rest == prefix {
			return true
		}
	}
	return strings.HasSuffix(rest, "_test.go") || slices.Contains(strings.Split(path.Dir(rest), "/"), "testdata")
}

// outputFile names the first changed file under dir that an output is
// built from, or "".
func outputFile(dir string, files []string) string {
	for _, file := range files {
		if rest, ok := strings.CutPrefix(file, dir+"/"); ok && !isNotOutput(rest) {
			return file
		}
	}
	return ""
}

// ReleaseChanged is true when a change goes into the component's
// outputs: a file in the directory of a component in its closure,
// except the paths in notOutputs, or the bake target of an image in
// its closure.
func ReleaseChanged(components map[string]*Component, name string, d Diff) (bool, string) {
	for _, n := range Closure(components, name) {
		what := outputFile(components[n].Dir, d.Files)
		if what == "" {
			what = targetChanged(components[n], d)
		}
		switch {
		case what == "":
			continue
		case n == name:
			return true, "changed: " + what
		default:
			return true, "its dependency " + n + " changed: " + what
		}
	}
	return false, ""
}

// CheckChanged is true when a change reaches the component's jobs:
//
//   - its part of a generated workflow;
//   - any file in its own directory;
//   - a file that an output of a component in its closure is built
//     from, because a component reads its dependencies only through
//     their builds;
//   - any file in brand/, when a component in its closure has a
//     manual, because every manual builds with brand's theme;
//   - the bake target of an image in its closure, or the part of the
//     bake file that every target shares when its closure has an image.
func CheckChanged(components map[string]*Component, name string, d Diff) (bool, string) {
	if file, ok := d.Workflows[name]; ok {
		return true, file
	}
	c := components[name]
	for _, file := range d.Files {
		if strings.HasPrefix(file, c.Dir+"/") {
			return true, file
		}
	}
	for _, n := range Closure(components, name) {
		dep := components[n]
		if file := outputFile(dep.Dir, d.Files); file != "" {
			return true, file
		}
		if brand := components["brand"]; brand != nil && dep.Docs != nil {
			for _, file := range d.Files {
				if strings.HasPrefix(file, brand.Dir+"/") {
					return true, file
				}
			}
		}
		if what := targetChanged(dep, d); what != "" {
			return true, what
		}
		if d.SharedBake && len(dep.Outputs.Images) > 0 {
			return true, bakeFile
		}
	}
	return false, ""
}

// targetChanged names the first image of the component whose bake
// target changed, or "".
func targetChanged(c *Component, d Diff) string {
	for _, image := range c.Outputs.Images {
		if d.Targets[image.Name] {
			return "the bake target " + image.Name + " in " + bakeFile
		}
	}
	return ""
}
