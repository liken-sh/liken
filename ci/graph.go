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

// everyJob is the prefix of the paths whose change runs every
// component's jobs: the workflows themselves.
const everyJob = ".github/"

// CheckPaths are the paths whose change runs the component's jobs:
// the directories of its closure, the workflows, and brand/ when the
// component has a manual, because every manual builds with brand's
// theme.
func CheckPaths(components map[string]*Component, name string) []string {
	paths := []string{everyJob}
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

// ReleaseChanged is true when a changed file goes into the component's
// outputs: a file in the directory of a component in its closure,
// except the paths in notOutputs.
func ReleaseChanged(components map[string]*Component, name string, changed []string) (bool, string) {
	for _, n := range Closure(components, name) {
		dir := components[n].Dir + "/"
		for _, file := range changed {
			rest, ok := strings.CutPrefix(file, dir)
			if !ok || isNotOutput(rest) {
				continue
			}
			if n == name {
				return true, "changed: " + file
			}
			return true, "its dependency " + n + " changed: " + file
		}
	}
	return false, ""
}

// CheckChanged is true when a changed file is under one of the
// component's check paths.
func CheckChanged(components map[string]*Component, name string, changed []string) (bool, string) {
	for _, path := range CheckPaths(components, name) {
		for _, file := range changed {
			if strings.HasPrefix(file, path) {
				return true, file
			}
		}
	}
	return false, ""
}

func isNotOutput(rest string) bool {
	for _, prefix := range notOutputs {
		if strings.HasSuffix(prefix, "/") && strings.HasPrefix(rest, prefix) || rest == prefix {
			return true
		}
	}
	return false
}
