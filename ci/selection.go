package main

import (
	"path"
	"path/filepath"
	"strings"
)

// An ImageRun is one entry of the matrix of a component's images job.
type ImageRun struct {
	Image  string `json:"image"`
	Pinned bool   `json:"pinned"`
	// Load is true for an image of one platform, which loads into the
	// runner's Docker daemon, so its smoke check can run.
	Load  bool   `json:"load"`
	Smoke string `json:"smoke"`
}

// everything lists every job and every image of the component.
func everything(c *Component) ([]string, []ImageRun) {
	var jobs []string
	for _, job := range c.Jobs {
		jobs = append(jobs, job.Name)
	}
	var images []ImageRun
	for _, image := range c.Outputs.Images {
		images = append(images, ImageRun{Image: image.Name, Pinned: c.Pinned(), Load: len(image.Platforms) <= 1, Smoke: image.Smoke})
	}
	return jobs, images
}

// A selector decides which of a component's jobs and images a change
// reaches, when the run does not publish the component.
type selector struct {
	root       string
	components map[string]*Component
	producer   map[string]string
	diff       Diff
}

// Select lists the jobs and the images that read the change.
//
// A job reads the whole component: a Go test can read a file of the
// manual, and a check of the manual reads the manifests. So each job
// runs when a file of the component changed, or a file of a dependency
// that the dependency's outputs are built from. A manual also builds
// with brand's theme, so a change to brand runs each hugo job of a
// component whose closure has a manual.
//
// An image reads only what BuildKit sends it: its build contexts, less
// what their ignore files leave out, its Dockerfile, its bake target,
// and the images it builds on. Its smoke check tests it too. So an
// image runs when one of those changed.
func (s selector) Select(c *Component) ([]string, []ImageRun) {
	code, theme := false, false
	for _, n := range Closure(s.components, c.Name()) {
		dep := s.components[n]
		if n == c.Name() && s.changedUnder(dep.Dir) || n != c.Name() && outputFile(dep.Dir, s.diff.Files) != "" {
			code = true
		}
		if brand := s.components["brand"]; brand != nil && dep.Docs != nil && s.changedUnder(brand.Dir) {
			theme = true
		}
	}
	var jobs []string
	for _, job := range c.Jobs {
		if code || theme && job.Toolchain == "hugo" {
			jobs = append(jobs, job.Name)
		}
	}
	_, all := everything(c)
	var images []ImageRun
	for i, image := range c.Outputs.Images {
		if s.imageReads(c, image, map[string]bool{}) {
			images = append(images, all[i])
		}
	}
	return jobs, images
}

func (s selector) changedUnder(dir string) bool {
	for _, file := range s.diff.Files {
		if strings.HasPrefix(file, dir+"/") {
			return true
		}
	}
	return false
}

// imageReads is true when the change reaches what the image's build
// reads. Anything it cannot read, such as a Dockerfile that does not
// parse, reads as a change, so the image builds and fails there.
func (s selector) imageReads(c *Component, image Image, seen map[string]bool) bool {
	if seen[image.Name] {
		return false
	}
	seen[image.Name] = true
	if s.diff.SharedBake || s.diff.Targets[image.Name] {
		return true
	}
	file := path.Join(c.Dir, orDefault(image.File, "Dockerfile"))
	context := path.Join(c.Dir, image.Context)
	for _, changed := range s.diff.Files {
		if changed == file || image.Smoke != "" && changed == image.Smoke {
			return true
		}
	}
	if s.contextChanged(context, ignoreFile(s.root, context, file)) {
		return true
	}
	for _, dir := range image.Contexts {
		if s.contextChanged(dir, path.Join(dir, ".dockerignore")) {
			return true
		}
	}
	parsed, err := readDockerfile(filepath.Join(s.root, file))
	if err != nil {
		return true
	}
	for _, base := range parsed.Bases(image, s.producer) {
		owner := s.components[s.producer[base]]
		for _, other := range owner.Outputs.Images {
			if other.Name == base && s.imageReads(owner, other, seen) {
				return true
			}
		}
	}
	return false
}

// contextChanged is true when a changed file is one that Docker sends
// in the build context: a file under dir that the ignore file keeps,
// and that an output is built from, or the ignore file itself.
func (s selector) contextChanged(dir, ignorePath string) bool {
	ignore, err := readIgnore(s.root, ignorePath)
	if err != nil {
		return true
	}
	for _, file := range s.diff.Files {
		if file == ignorePath {
			return true
		}
		rest, ok := strings.CutPrefix(file, dir+"/")
		if !ok {
			continue
		}
		if owner := s.components[ownerOf(s.components, file)]; owner != nil && isNotOutput(strings.TrimPrefix(file, owner.Dir+"/")) {
			continue
		}
		if skip, err := ignore.MatchesOrParentMatches(rest); err != nil || !skip {
			return true
		}
	}
	return false
}
