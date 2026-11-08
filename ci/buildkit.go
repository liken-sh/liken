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

// buildkitKey is the entry of imagesFile that names the BuildKit daemon
// image. docker/setup-buildx-action creates a builder with the
// docker-container driver, which runs BuildKit in a container, and
// every image that CI builds and publishes comes out of that daemon.
// With no image in the step's driver-opts, the action and buildx both
// pull moby/buildkit:buildx-stable-1, a tag that BuildKit moves to each
// new release. So the daemon that builds every image would change with
// no change to the tree. Each generated setup-buildx-action step names
// this entry's digest in driver-opts, and checkBuildKit fails any
// workflow or composite action whose step names another image or none.
const buildkitKey = "buildkit"

// setupBuildx is the action that creates each CI job's builder.
const setupBuildx = "docker/setup-buildx-action"

// readBuildKit reads the reference of buildkitKey from imagesFile.
func readBuildKit(root string) (string, error) {
	tools, err := readToolchains(root)
	if err != nil {
		return "", err
	}
	image := tools.Build[buildkitKey]
	if image == "" {
		return "", fmt.Errorf("%s names no %s image, so CI's builders would run BuildKit's moving default image", imagesFile, buildkitKey)
	}
	return image, nil
}

// checkBuildKit lists each setup-buildx-action step under .github that
// does not start the BuildKit image of imagesFile: the generated
// workflows, the hand-written workflows, and each composite action.
// A tree whose imagesFile names no BuildKit image fails on its first
// setup-buildx-action step, and a tree with no such step needs no pin.
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
			// git still tracks a file that the tree deleted.
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
// or composite action whose driver-opts name no image, or an image
// other than the pinned one. A workflow's steps are under jobs, and a
// composite action's are under runs.
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
		return nil, fmt.Errorf("%s: %w", file, err)
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
			return nil, fmt.Errorf("%s:%d: %w", file, node.Line, err)
		}
		if !strings.HasPrefix(step.Uses, setupBuildx+"@") {
			continue
		}
		named := driverImage(fmt.Sprint(step.With["driver-opts"]))
		switch named {
		case image:
			continue
		case "":
			findings = append(findings, fmt.Sprintf("%s:%d runs %s with no image in driver-opts, so its builder runs moby/buildkit:buildx-stable-1, a moving tag; write driver-opts: image=%s",
				file, node.Line, setupBuildx, image))
		default:
			findings = append(findings, fmt.Sprintf("%s:%d runs %s with the BuildKit image %s, and %s pins %s as %s",
				file, node.Line, setupBuildx, named, imagesFile, buildkitKey, image))
		}
	}
	return findings, nil
}

// driverImage is the image that a driver-opts input names, one option
// on each line, the way setup-buildx-action reads the input.
func driverImage(opts string) string {
	for _, opt := range strings.Split(opts, "\n") {
		if image, ok := strings.CutPrefix(strings.TrimSpace(opt), "image="); ok {
			return image
		}
	}
	return ""
}
