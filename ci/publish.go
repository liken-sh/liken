package main

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// devVersion is the grammar of a development version.
var devVersion = regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}-dev-[0-9]{3,}-[0-9a-f]{8}$`)

// source is the repository that every image and artifact names as its
// source. ghcr links a new package to the repository that its
// org.opencontainers.image.source label names.
const source = "https://github.com/liken-sh/liken"

// A Runner runs one command in a directory, or prints it in a dry run.
type Runner func(dir, name string, args ...string) error

// ExecRunner runs each command with its output on this program's.
func ExecRunner(dir, name string, args ...string) error {
	fmt.Printf("+ %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// Publisher pushes one component's images and deploy artifact.
type Publisher struct {
	Root     string
	Registry Registry
	Run      Runner
	// Commit is the full sha that the outputs are built from.
	Commit string
}

// Publish pushes the component's outputs under the version: every
// image first, then the deploy artifact, because a cluster follows the
// deploy artifact and must never read one that names an image that is
// not pushed yet.
//
// A version that is already published stays as it is. A run of the
// same tag again, after a failure, pushes only what the failed run did
// not.
//
// A release also moves each :latest tag, unless a newer release
// already has it. A development build never moves :latest.
func (p Publisher) Publish(c *Component, version, mode string) error {
	switch mode {
	case publishRelease:
		if err := CheckReleaseTag(version); err != nil {
			return err
		}
	case publishDev:
		if !devVersion.MatchString(version) {
			return fmt.Errorf("%q is not a development version", version)
		}
	default:
		return fmt.Errorf("the publish mode %q is not dev or release", mode)
	}
	if c.Outputs.Channel {
		return fmt.Errorf("%s publishes to the release channel through its own workflow", c.Name())
	}
	for _, image := range c.Outputs.Images {
		if err := p.image(c, image, version, mode); err != nil {
			return err
		}
	}
	if c.Outputs.Deploy != "" {
		return p.deploy(c, version, mode)
	}
	return nil
}

func ref(name, tag string) string { return "ghcr.io/liken-sh/" + name + ":" + tag }

func (p Publisher) image(c *Component, image Image, version, mode string) error {
	tags, err := p.Registry.Tags(image.Name)
	if err != nil {
		return err
	}
	if slices.Contains(tags, version) {
		fmt.Printf("%s is already published; a published version never changes\n", ref(image.Name, version))
	} else {
		args := []string{"buildx", "build", "--push",
			"--file", filepath.Join(c.Dir, orDefault(image.File, "Dockerfile")),
			"--build-arg", "VERSION=" + version,
			"--cache-from", "type=registry,ref=" + ref(image.Name, "buildcache"),
			"--label", "org.opencontainers.image.source=" + source,
			"--label", "org.opencontainers.image.revision=" + p.Commit,
			"--label", "org.opencontainers.image.version=" + version,
		}
		platforms := image.Platforms
		if len(platforms) == 0 {
			platforms = []string{"linux/amd64"}
		}
		args = append(args, "--platform", strings.Join(platforms, ","))
		if image.Target != "" {
			args = append(args, "--target", image.Target)
		}
		for _, name := range sortedKeys(image.Contexts) {
			args = append(args, "--build-context", name+"="+image.Contexts[name])
		}
		for _, name := range append([]string{image.Name}, image.Aliases...) {
			args = append(args, "--tag", ref(name, version))
		}
		args = append(args, filepath.Join(c.Dir, image.Context))
		if err := p.Run(p.Root, "docker", args...); err != nil {
			return fmt.Errorf("pushing %s: %w", ref(image.Name, version), err)
		}
	}
	if mode != publishRelease || version < NewestRelease(tags) {
		return nil
	}
	for _, name := range append([]string{image.Name}, image.Aliases...) {
		if err := p.Run(p.Root, "docker", "buildx", "imagetools", "create",
			"--tag", ref(name, "latest"), ref(image.Name, version)); err != nil {
			return fmt.Errorf("moving %s: %w", ref(name, "latest"), err)
		}
	}
	return nil
}

func (p Publisher) deploy(c *Component, version, mode string) error {
	pkg := c.DeployPackage()
	tags, err := p.Registry.Tags(pkg)
	if err != nil {
		return err
	}
	artifact := "oci://" + ref(pkg, version)
	if slices.Contains(tags, version) {
		fmt.Printf("%s is already published; a published version never changes\n", artifact)
	} else {
		dir, err := os.MkdirTemp("", pkg)
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		if err := os.CopyFS(dir, os.DirFS(filepath.Join(p.Root, c.Dir, c.Outputs.Deploy))); err != nil {
			return err
		}
		if err := StampImages(dir, c, version); err != nil {
			return err
		}
		if err := p.Run(p.Root, "flux", "push", "artifact", artifact,
			"--path", dir,
			"--source", source,
			"--revision", version+"@sha1:"+p.Commit); err != nil {
			return fmt.Errorf("pushing %s: %w", artifact, err)
		}
	}
	if mode != publishRelease || version < NewestRelease(tags) {
		return nil
	}
	return p.Run(p.Root, "flux", "tag", "artifact", artifact, "--tag", "latest")
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// StampImages sets the tag of each of the component's own images in
// every kustomization.yaml under dir to the version. The published
// manifests then name the images that were pushed with them, so a
// cluster that follows the deploy artifact moves the manifests and
// the images in one step. An image of another project, such as
// mosquitto, keeps the tag its manifest states.
func StampImages(dir string, c *Component, version string) error {
	own := map[string]bool{}
	for _, name := range c.Packages() {
		own["ghcr.io/liken-sh/"+name] = true
	}
	return filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() != "kustomization.yaml" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if !stampNode(&doc, own, version) {
			return nil
		}
		var out strings.Builder
		encoder := yaml.NewEncoder(&out)
		encoder.SetIndent(2)
		if err := encoder.Encode(&doc); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(out.String()), 0o644)
	})
}

// stampNode sets newTag on each entry of the kustomization's images
// list whose name is one of the component's images, and reports
// whether it changed anything.
func stampNode(doc *yaml.Node, own map[string]bool, version string) bool {
	if len(doc.Content) == 0 {
		return false
	}
	root := doc.Content[0]
	changed := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "images" {
			continue
		}
		for _, entry := range root.Content[i+1].Content {
			if !own[mappingValue(entry, "name")] {
				continue
			}
			setMappingValue(entry, "newTag", version)
			changed = true
		}
	}
	return changed
}

func mappingValue(m *yaml.Node, key string) string {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1].Value
		}
	}
	return ""
}

func setMappingValue(m *yaml.Node, key, value string) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1].Value = value
			m.Content[i+1].Tag = "!!str"
			m.Content[i+1].Style = 0
			return
		}
	}
	m.Content = append(m.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}
