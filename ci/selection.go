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
	// Cache is true for an image whose cache mount the job restores
	// before the build and saves after it.
	Cache bool `json:"cache"`
	// Published is true for an image of a pinned component whose tag
	// is published with the tree's recipe. The job pulls that image and
	// runs its smoke check, and builds nothing.
	Published bool `json:"published"`
}

func everything(c *Component) ([]string, []ImageRun) {
	var jobs []string
	for _, job := range c.Jobs {
		jobs = append(jobs, job.Name)
	}
	var images []ImageRun
	for _, image := range c.Outputs.Images {
		images = append(images, ImageRun{Image: image.Name, Pinned: c.Pinned(), Load: len(image.Platforms) <= 1, Smoke: image.Smoke, Cache: image.CacheMount})
	}
	return jobs, images
}

// A selector works out which of a component's jobs and images a
// change affects.
type selector struct {
	root       string
	components map[string]*Component
	producer   map[string]string
	diff       Diff
}

// Select returns the jobs and images that the change affects.
//
// A job runs when a changed file is one that its toolchain reads, as
// reads.go describes.
//
// An image runs when the change touches anything its build receives:
// its Dockerfile, its build contexts minus the files their ignore files
// exclude, its bake target or the shared part of the bake file, or an
// image it builds on. A change to its smoke check, or to a helper file
// beside the check, also runs it.
//
// When the run publishes the component, Select also returns each image
// that reads the VERSION build argument, because the publish gives it a
// new version and so new inputs. The images job then runs its smoke
// check at the version that the publish pushes, and caches the layers
// that the publish builds from. An untouched image that does not read
// VERSION has the same inputs as in an earlier run that passed, so the
// publish builds it from the layers that run cached.
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

// imageReads returns true when the change touches anything the image's
// build receives, or when it cannot tell, as with a Dockerfile that does
// not parse, so that the image builds and the build reports the failure.
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

// smokeReads returns true when a changed file is the image's smoke check, or
// is in the same directory and is not another image's smoke check, such
// as a helper script that the check calls.
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

// contextChanged returns true when a changed file is part of the build
// context that Docker sends: a file under dir that the ignore file does
// not exclude and that is an input of its component's outputs, or the
// ignore file itself.
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
