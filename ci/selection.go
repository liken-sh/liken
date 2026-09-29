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
// reaches.
type selector struct {
	root       string
	components map[string]*Component
	producer   map[string]string
	diff       Diff
}

// Select lists the jobs and the images that read the change.
//
// A job runs when a changed file is one that its toolchain reads, as
// reads.go describes.
//
// An image reads only what BuildKit sends it: its build contexts, less
// what their ignore files leave out, its Dockerfile, its bake target,
// and the images it builds on. Its smoke check tests it too. So an
// image runs when one of those changed.
//
// A run that publishes the component also runs each image that reads
// the VERSION build argument, because the publish gives it a new
// version, and a new version is a change to what the image is built
// from. The image then passes its smoke check at the version that the
// publish pushes, and the publish builds it from the layers that its
// run here wrote to the cache. An image that reads neither the version
// nor any other part of the change has the inputs of the image that an
// earlier green run built, and the publish builds it again from the
// layers that run wrote to the cache.
func (s selector) Select(c *Component, publishing bool) ([]string, []ImageRun) {
	var jobs []string
	for _, job := range c.Jobs {
		deps := s.jobDeps(c, job)
		for _, file := range s.diff.Files {
			if s.jobReads(c, job, deps, file) {
				jobs = append(jobs, job.Name)
				break
			}
		}
	}
	_, all := everything(c)
	var images []ImageRun
	for i, image := range c.Outputs.Images {
		if s.imageReads(c, image, map[string]bool{}) || publishing && s.readsVersion(c, image, map[string]bool{}) {
			images = append(images, all[i])
		}
	}
	return jobs, images
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
		if changed == file || s.smokeReads(image, changed) {
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

// smokeReads is true when a changed file is the image's smoke check, or
// a file beside it that is no other image's smoke check, such as a
// helper the check reads.
func (s selector) smokeReads(image Image, changed string) bool {
	if image.Smoke == "" {
		return false
	}
	if changed == image.Smoke {
		return true
	}
	if !strings.HasPrefix(changed, path.Dir(image.Smoke)+"/") {
		return false
	}
	for _, c := range s.components {
		for _, other := range c.Outputs.Images {
			if other.Smoke == changed {
				return false
			}
		}
	}
	return true
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
		if owner := s.components[ownerOf(s.components, file)]; owner != nil && owner.isNotOutput(strings.TrimPrefix(file, owner.Dir+"/")) {
			continue
		}
		if skip, err := ignore.MatchesOrParentMatches(rest); err != nil || !skip {
			return true
		}
	}
	return false
}
