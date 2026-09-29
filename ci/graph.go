package main

import (
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

// CheckPaths are the paths whose change runs the component's jobs:
// the directories of its closure, and brand/ when the component has a
// manual, because every manual builds with brand's theme.
func CheckPaths(components map[string]*Component, name string) []string {
	var paths []string
	for _, n := range Closure(components, name) {
		paths = append(paths, components[n].Dir+"/")
		if components[n].Docs != nil && components["brand"] != nil {
			paths = append(paths, components["brand"].Dir+"/")
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// notOutputs are the paths inside a component that no output is built
// from: the manual, the plans, and the agent and reader notes. A
// change to them runs the component's jobs, and it does not give the
// component a new version.
var notOutputs = []string{"docs/", "plans/", "AGENTS.md", "README.md"}

// ReleaseChanged is true when a change goes into the component's
// outputs: a file in the directory of a component in its closure,
// except the paths in notOutputs, or the bake target of an image in
// its closure.
func ReleaseChanged(components map[string]*Component, name string, d Diff) (bool, string) {
	for _, n := range Closure(components, name) {
		dir := components[n].Dir + "/"
		what := ""
		for _, file := range d.Files {
			if rest, ok := strings.CutPrefix(file, dir); ok && !isNotOutput(rest) {
				what = file
				break
			}
		}
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

// CheckChanged is true when a change reaches the component's jobs: its
// part of a generated workflow, a file under one of its check paths,
// the bake target of an image in its closure, or the part of the bake
// file that every target shares when its closure has an image.
func CheckChanged(components map[string]*Component, name string, d Diff) (bool, string) {
	if file, ok := d.Workflows[name]; ok {
		return true, file
	}
	for _, path := range CheckPaths(components, name) {
		for _, file := range d.Files {
			if strings.HasPrefix(file, path) {
				return true, file
			}
		}
	}
	for _, n := range Closure(components, name) {
		if what := targetChanged(components[n], d); what != "" {
			return true, what
		}
		if d.SharedBake && len(components[n].Outputs.Images) > 0 {
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

func isNotOutput(rest string) bool {
	for _, prefix := range notOutputs {
		if strings.HasSuffix(prefix, "/") && strings.HasPrefix(rest, prefix) || rest == prefix {
			return true
		}
	}
	return false
}
