package main

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
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

// plansDir holds a component's plans. A change to a plan runs none of
// the component's jobs, because no build, test, or drill reads a plan.
// The whitespace hooks are the only checks of a plan. They run at
// commit time, and over every file whenever the component's prek job
// runs.
const plansDir = "plans/"

// notOutputs are the paths at the top of a component that no output is
// built from, so a change to one of them does not release the
// component. A change to any of them except plans/ still runs the
// component's own jobs.
//
// The skills in skills/ are generated from the guides of the manual, and
// the repository keeps them for agents to read. In brand, skills/ holds
// the generator, and only the builds of the manuals run it. smoke/ holds
// checks that test an image and are not copied into it. The hooks, the
// coverage floor, and .gitignore configure the checks and git, and no
// build reads them. .dockerignore is not in the list, because it sets
// which files an image's build context sends.
var notOutputs = []string{"docs/", plansDir, "AGENTS.md", "README.md", "skills/", "smoke/",
	".pre-commit-config.yaml", ".testcoverage.yml", ".gitignore"}

// isNotOutput is true for a path, relative to the component, that no
// output is built from: a path in notOutputs or in the component's
// excluded paths, a Go test file, or a file under a testdata directory
// at any depth. Go compiles a test file and reads test data only for
// the tests of its own package, so neither one goes into a binary.
func (c *Component) isNotOutput(rest string) bool {
	for _, prefix := range slices.Concat(notOutputs, c.Outputs.Exclude) {
		if strings.HasSuffix(prefix, "/") && strings.HasPrefix(rest, prefix) || rest == prefix {
			return true
		}
	}
	return strings.HasSuffix(rest, "_test.go") || slices.Contains(strings.Split(path.Dir(rest), "/"), "testdata")
}

// within is true when the path a is b or a path under b.
func within(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+"/")
}

// validateReads checks that each path in a job's reads is a clean path
// to a file of another component. A file of the job's own component
// already runs the job, and a path that matches no file would never run
// it.
func validateReads(root string, components map[string]*Component) error {
	for _, c := range sortedComponents(components) {
		for _, job := range c.Jobs {
			for _, file := range job.Reads {
				owner := ownerOf(components, file)
				if owner == "" || owner == c.Name() || path.Clean(file) != file {
					return fmt.Errorf("%s: job %q reads %q, which is not a file of another component", c.Name(), job.Name, file)
				}
				info, err := os.Stat(filepath.Join(root, file))
				if err != nil || info.IsDir() {
					return fmt.Errorf("%s: job %q reads %q, which is not a file", c.Name(), job.Name, file)
				}
			}
		}
	}
	return nil
}

// validateExcluded fails when a path in a component's outputs.exclude
// is an input to the build of an image, as its Dockerfile or as a file that a
// build context sends. A change to that path would then change the image
// without a release of its component. Each image of every component is
// checked, because an image can take another component's directory as a
// named context.
func validateExcluded(root string, components map[string]*Component) error {
	for _, c := range sortedComponents(components) {
		for _, excluded := range c.Outputs.Exclude {
			target := path.Join(c.Dir, excluded)
			for _, owner := range sortedComponents(components) {
				for _, image := range owner.Outputs.Images {
					file, err := imageReceives(root, owner, image, target)
					if err != nil {
						return err
					}
					if file != "" {
						return fmt.Errorf("%s excludes %q, but the image %s receives %s", c.Name(), excluded, image.Name, file)
					}
				}
			}
		}
	}
	return nil
}

// imageReceives returns the first file at or under target that the
// image's build reads: its Dockerfile, or a file that one of its build
// contexts sends. It reads the same ignore files as Recipe and the
// selector in selection.go.
func imageReceives(root string, c *Component, image Image, target string) (string, error) {
	dockerfile := path.Join(c.Dir, orDefault(image.File, "Dockerfile"))
	if within(dockerfile, target) {
		return dockerfile, nil
	}
	context := path.Join(c.Dir, image.Context)
	contexts := map[string]string{context: ignoreFile(root, context, dockerfile)}
	for _, dir := range image.Contexts {
		contexts[dir] = path.Join(dir, ".dockerignore")
	}
	for _, dir := range slices.Sorted(maps.Keys(contexts)) {
		file, err := contextSends(root, dir, contexts[dir], target)
		if file != "" || err != nil {
			return file, err
		}
	}
	return "", nil
}

// contextSends returns the first file at or under target that the
// build context dir sends, or "".
func contextSends(root, dir, ignorePath, target string) (string, error) {
	start := target
	switch {
	case within(dir, target):
		start = dir
	case !within(target, dir):
		return "", nil
	}
	ignore, err := readIgnore(root, ignorePath)
	if err != nil {
		return "", err
	}
	var sent string
	err = filepath.WalkDir(filepath.Join(root, start), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || sent != "" {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		skip, err := ignore.MatchesOrParentMatches(strings.TrimPrefix(rel, dir+"/"))
		if err != nil || !skip {
			sent = rel
		}
		return nil
	})
	return sent, err
}

// outputFile returns the first of the changed files that an output of
// the component is built from, or "".
func outputFile(c *Component, files []string) string {
	for _, file := range files {
		if rest, ok := strings.CutPrefix(file, c.Dir+"/"); ok && !c.isNotOutput(rest) {
			return file
		}
	}
	return ""
}

// ReleaseChanged reports whether the diff changes an output of the
// component, and why. A change counts when it is to a file that an
// output of a component in the closure is built from, or to the bake
// target of an image in the closure.
func ReleaseChanged(components map[string]*Component, name string, d Diff) (bool, string) {
	for _, n := range Closure(components, name) {
		what := outputFile(components[n], d.outputFiles())
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

// CheckChanged reports whether the diff runs the component's jobs, and
// the file that runs them. These changes run them:
//
//   - its part of a generated workflow
//   - any file in its own directory, except its plans
//   - a file that one of its jobs lists in reads
//   - a file that an output of a component in its closure is built
//     from, because a component reads its dependencies only through
//     their builds
//   - any file in brand/ except its plans, when a component in its
//     closure has a manual, because every manual builds with brand's
//     theme
//   - the bake target of an image in its closure, or the part of the
//     bake file that every target shares, when its closure has an image
func CheckChanged(components map[string]*Component, name string, d Diff) (bool, string) {
	if file, ok := d.Workflows[name]; ok {
		return true, file
	}
	c := components[name]
	for _, file := range d.Files {
		if strings.HasPrefix(file, c.Dir+"/") && !strings.HasPrefix(file, c.Dir+"/"+plansDir) {
			return true, file
		}
		for _, job := range c.Jobs {
			if slices.Contains(job.Reads, file) {
				return true, file
			}
		}
	}
	for _, n := range Closure(components, name) {
		dep := components[n]
		if file := outputFile(dep, d.outputFiles()); file != "" {
			return true, file
		}
		if brand := components["brand"]; brand != nil && dep.Docs != nil {
			for _, file := range d.Files {
				if strings.HasPrefix(file, brand.Dir+"/") && !strings.HasPrefix(file, brand.Dir+"/"+plansDir) {
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

// targetChanged describes the first image of the component whose bake
// target changed, or returns "".
func targetChanged(c *Component, d Diff) string {
	for _, image := range c.Outputs.Images {
		if d.Targets[image.Name] {
			return "the bake target " + image.Name + " in " + bakeFile
		}
	}
	return ""
}
