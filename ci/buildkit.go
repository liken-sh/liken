package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// buildkitKey is the entry of imagesFile that pins the BuildKit image.
// docker/setup-buildx-action creates a builder with the docker-container
// driver, which runs BuildKit in a container, and that daemon builds
// every image that CI publishes. When driver-opts sets no image, buildx
// pulls moby/buildkit:buildx-stable-1, a tag that moves to each BuildKit
// release, so the daemon would change with no commit to the tree. So
// each generated setup-buildx-action step sets this image in
// driver-opts, and checkBuildKit fails a step that sets another image
// or none.
const buildkitKey = "buildkit"

const setupBuildx = "docker/setup-buildx-action"

// readBuildKit returns the image reference that imagesFile pins under
// buildkitKey.
func readBuildKit(root string) (string, error) {
	tools, err := readToolchains(root)
	if err != nil {
		return "", err
	}
	image := tools.Build[buildkitKey]
	if image == "" {
		return "", fmt.Errorf("%s has no %s image, so CI's builders would run BuildKit's moving default image", imagesFile, buildkitKey)
	}
	return image, nil
}

// checkBuildKit lists each setup-buildx-action step in a workflow or a
// composite action under .github that does not run the BuildKit image
// that imagesFile pins. When imagesFile pins no BuildKit image, each
// step that sets an image in driver-opts is a finding, and a step that
// sets none is not.
func checkBuildKit(root string) ([]string, error) {
	tools, err := readToolchains(root)
	if err != nil {
		return nil, err
	}
	image := tools.Build[buildkitKey]
	files, err := sourceFiles(root)
	if err != nil {
		return nil, err
	}
	var findings []string
	for _, file := range files {
		if !strings.HasPrefix(file, ".github/") || (path.Ext(file) != ".yaml" && path.Ext(file) != ".yml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, file))
		if errors.Is(err, fs.ErrNotExist) {
			// git ls-files still lists a file that was deleted from the
			// working tree and not from the index.
			continue
		}
		if err != nil {
			return nil, err
		}
		found, err := unpinnedBuilders(file, data, image)
		if err != nil {
			return nil, err
		}
		findings = append(findings, found...)
	}
	return findings, nil
}

// unpinnedBuilders lists each setup-buildx-action step of one workflow
// or composite action whose driver-opts do not set image=<image>.
// A workflow's steps are under jobs, and a composite action's are under
// runs.
func unpinnedBuilders(file string, data []byte, image string) ([]string, error) {
	var doc struct {
		Jobs map[string]struct {
			Steps []yaml.Node `yaml:"steps"`
		} `yaml:"jobs"`
		Runs struct {
			Steps []yaml.Node `yaml:"steps"`
		} `yaml:"runs"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", file, err)
	}
	steps := doc.Runs.Steps
	for _, job := range doc.Jobs {
		steps = append(steps, job.Steps...)
	}
	slices.SortFunc(steps, func(a, b yaml.Node) int { return a.Line - b.Line })
	var findings []string
	for _, node := range steps {
		var step struct {
			Uses string         `yaml:"uses"`
			With map[string]any `yaml:"with"`
		}
		if err := node.Decode(&step); err != nil {
			return nil, fmt.Errorf("decoding the step at %s:%d: %w", file, node.Line, err)
		}
		if !strings.HasPrefix(step.Uses, setupBuildx+"@") {
			continue
		}
		named := driverImage(fmt.Sprint(step.With["driver-opts"]))
		switch named {
		case image:
			continue
		case "":
			findings = append(findings, fmt.Sprintf("%s:%d runs %s with no image in driver-opts, so its builder runs moby/buildkit:buildx-stable-1, a moving tag. Write driver-opts: image=%s",
				file, node.Line, setupBuildx, image))
		default:
			findings = append(findings, fmt.Sprintf("%s:%d runs %s with the BuildKit image %s, and %s pins %s as %s",
				file, node.Line, setupBuildx, named, imagesFile, buildkitKey, image))
		}
	}
	return findings, nil
}

// driverImage returns the value of the image option in a driver-opts
// input, or "" when there is none. setup-buildx-action reads the input
// as one option on each line.
func driverImage(opts string) string {
	for _, opt := range strings.Split(opts, "\n") {
		if image, ok := strings.CutPrefix(strings.TrimSpace(opt), "image="); ok {
			return image
		}
	}
	return ""
}
