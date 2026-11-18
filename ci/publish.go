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
	"time"

	"gopkg.in/yaml.v3"
)

// devVersion matches the version of a development build, such as
// 2026.10.02-001-dev-042-0123abcd.
var devVersion = regexp.MustCompile(`^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}-dev-[0-9]{3,}-[0-9a-f]{8}$`)

// source is the repository that every image and artifact lists as its
// source. ghcr.io links a new package to the repository named in its
// org.opencontainers.image.source label.
const source = "https://github.com/liken-sh/liken"

// A Runner runs one command in a directory. Tests replace it with a
// recorder.
type Runner func(dir, name string, args ...string) error

// ExecRunner runs the command, prints it first, and connects its output
// to this program's.
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
	// Created is the committer time of Commit. Each output records it as
	// org.opencontainers.image.created. The publish does not pass it to
	// the build as SOURCE_DATE_EPOCH, because that build argument changes
	// the cache key of each WORKDIR step, and the publish would miss the
	// layer cache that the images job writes. So the created field in
	// the image config is the time of the build.
	Created time.Time
	// Components is every component in the repository, not only the one
	// being published. The recipe of a pinned component includes the
	// tags of its dependencies, and an image's labels include those of
	// the images it builds on.
	Components map[string]*Component
	// SARIF is the directory where the Trivy scan of each pushed image
	// writes its report, or "" for no scan. See scan.
	SARIF string
	// DockerConfig is the directory of the Docker configuration with the
	// registry credential. cosign uses it to push each signature. See
	// sign.
	DockerConfig string
}

// Publish pushes the component's outputs under the version. It pushes
// every image before the deploy artifact, because a cluster follows the
// deploy artifact, and the artifact must not name an image that is not
// pushed yet. Publish does not push a version that is already
// published, so when a run fails and runs again, the second run pushes
// only what the first run did not.
//
// A release moves each :latest tag to the new version, unless a newer
// release already has it. A development build of a component that is
// not pinned never moves :latest.
//
// A pinned component publishes only the tag that the tree pins, with its
// recipe hash as a label, and a dev or release publish then moves
// :latest to that tag. No build in the repository reads :latest,
// because each one uses a base by its digest or builds it from the
// tree, but a person who pulls a base by hand does. The plan job
// publishes a pin only when its tag is not published yet, so :latest
// points to the pin that was published last.
//
// A dry run builds each image with the same arguments, labels, and tags
// as a real publish and stamps the deploy artifact, but it pushes
// nothing and moves no tag. So a change to the publish code can be
// tested before a release depends on it.
func (p Publisher) Publish(c *Component, version, mode string) error {
	if c.Pinned() {
		return p.pinned(c, version, mode)
	}
	switch mode {
	case publishRelease:
		if err := CheckReleaseTag(version); err != nil {
			return err
		}
	case publishDev:
		if !devVersion.MatchString(version) {
			return fmt.Errorf("the version %q does not match a development version, which looks like 2026.10.02-001-dev-004-0123abcd", version)
		}
	case publishDry:
		if !devVersion.MatchString(version) && CheckReleaseTag(version) != nil {
			return fmt.Errorf("the version %q is neither a development version nor a release version", version)
		}
	default:
		return fmt.Errorf("the publish mode %q is unknown, want dev, release, or dry", mode)
	}
	if c.Outputs.Channel {
		return fmt.Errorf("%s publishes to the release channel through its own workflow, so this command does not publish it", c.Name())
	}
	for _, image := range c.Outputs.Images {
		if err := p.image(c, image, version, mode, ""); err != nil {
			return err
		}
	}
	if c.Outputs.Deploy != "" {
		return p.deploy(c, version, mode)
	}
	return nil
}

func (p Publisher) pinned(c *Component, version, mode string) error {
	if mode != publishDev && mode != publishRelease && mode != publishDry {
		return fmt.Errorf("the publish mode %q is unknown, want dev, release, or dry", mode)
	}
	if version != c.PinnedTag() {
		return fmt.Errorf("%s is pinned at %s, and the version is %s", c.Name(), c.PinnedTag(), version)
	}
	recipe, err := Recipe(p.Root, p.Components, c)
	if err != nil {
		return err
	}
	for _, image := range c.Outputs.Images {
		if err := p.image(c, image, version, mode, recipe); err != nil {
			return err
		}
	}
	return nil
}

func ref(name, tag string) string { return "ghcr.io/liken-sh/" + name + ":" + tag }

func digestRef(name, digest string) string { return "ghcr.io/liken-sh/" + name + "@" + digest }

// image builds and pushes one image through the bake file, so an image
// that builds on another image of the repository builds on the one at
// this commit. The bake file sets the target's build contexts,
// platforms, and layer cache. Bake pushes only the target named on the
// command line. A base image that the build needs is built with it and
// stays in the builder.
//
// The push includes an SBOM and the provenance. Then the publish reads
// the pushed image's labels back from the registry and checks them (see
// checkLabels), and cosign signs the pushed digest under each name of
// the image. Both happen before anything else uses the image: the scan,
// the move of :latest, and the deploy artifact. When either fails, the
// publish fails. The next run for the same version finds the image
// published, checks its labels, and signs it by its tag. cosign
// resolves the tag to the digest that the failed run pushed, because a
// published version tag never moves.
//
// A dry run exports the image into the builder and pushes nothing. The
// exporter writes the index and its annotations, so a dry run fails on
// any annotation that a push would reject.
func (p Publisher) image(c *Component, image Image, version, mode, recipe string) error {
	tools, err := readPublishTools(p.Root)
	if err != nil {
		return err
	}
	tags, err := p.Registry.Tags(image.Name)
	if err != nil {
		return err
	}
	metadata, err := p.metadata(c, image, version, recipe)
	if err != nil {
		return err
	}
	names := append([]string{image.Name}, image.Aliases...)
	dry := mode == publishDry
	if slices.Contains(tags, version) && dry && c.Pinned() {
		fmt.Printf("%s is already published, so the dry run builds nothing\n", ref(image.Name, version))
		return nil
	}
	if slices.Contains(tags, version) && !dry {
		fmt.Printf("%s is already published, and a published version never changes\n", ref(image.Name, version))
		if err := p.checkLabels(image, version, metadata.Labels); err != nil {
			return err
		}
		var references []string
		for _, name := range names {
			references = append(references, ref(name, version))
		}
		if err := p.sign(tools.Cosign, references...); err != nil {
			return err
		}
	} else {
		target := image.Name
		set := func(key, value string) []string { return []string{"--set", target + "." + key + "=" + value} }
		args := []string{"buildx", "bake", "--file", bakeFile}
		// The override file makes the build use the published image of
		// each pinned base that passed its checks. Without the file, the
		// build builds every base from the tree.
		if _, err := os.Stat(filepath.Join(p.Root, overrideFile)); err == nil {
			args = append(args, "--file", overrideFile)
		}
		if dry {
			args = append(args, set("output", "type=image,push=false")...)
		} else {
			args = append(args, "--push")
		}
		if !c.Pinned() {
			args = append(args, set("args.VERSION", version)...)
		}
		args = append(args, metadata.bakeArgs(target)...)
		for _, name := range names {
			args = append(args, set("tags", ref(name, version))...)
		}
		metadataFile, err := os.CreateTemp("", image.Name+"-metadata-*.json")
		if err != nil {
			return err
		}
		metadataFile.Close()
		defer os.Remove(metadataFile.Name())
		args = append(args, tools.attestArgs(target)...)
		args = append(args, "--metadata-file", metadataFile.Name(), target)
		if err := p.Run(p.Root, "docker", args...); err != nil {
			return fmt.Errorf("pushing %s: %w", ref(image.Name, version), err)
		}
		if !dry {
			digest, err := pushedDigest(metadataFile.Name(), target)
			if err != nil {
				return fmt.Errorf("pushing %s: %w", ref(image.Name, version), err)
			}
			if err := p.checkPushedLabels(image, digest, metadata.Labels); err != nil {
				return err
			}
			var references []string
			for _, name := range names {
				references = append(references, digestRef(name, digest))
			}
			if err := p.sign(tools.Cosign, references...); err != nil {
				return err
			}
			p.scan(image, version)
		}
	}
	moves := c.Pinned() || (mode == publishRelease && version >= NewestRelease(tags))
	if dry || !moves {
		return nil
	}
	for _, name := range names {
		if err := p.Run(p.Root, "docker", "buildx", "imagetools", "create",
			"--tag", ref(name, "latest"), ref(image.Name, version)); err != nil {
			return fmt.Errorf("moving %s: %w", ref(name, "latest"), err)
		}
	}
	return nil
}

// scan runs Trivy on an image that this run pushed. An image that was
// already published is not pushed again, so it is not scanned again.
// scanImage explains why a failed scan does not fail the publish.
func (p Publisher) scan(image Image, version string) {
	if p.SARIF != "" {
		scanImage(p.Run, p.Root, p.SARIF, image.Name, version)
	}
}

func (p Publisher) deploy(c *Component, version, mode string) error {
	pkg := c.DeployPackage()
	tags, err := p.Registry.Tags(pkg)
	if err != nil {
		return err
	}
	artifact := "oci://" + ref(pkg, version)
	if slices.Contains(tags, version) && mode != publishDry {
		fmt.Printf("%s is already published, and a published version never changes\n", artifact)
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
		args := []string{"push", "artifact", artifact,
			"--path", dir,
			"--source", source,
			"--revision", version + "@sha1:" + p.Commit}
		annotations, err := p.deployAnnotations(c, version)
		if err != nil {
			return err
		}
		args = append(args, annotations...)
		if mode == publishDry {
			fmt.Printf("a publish would push %s from %s\n", artifact, dir)
			return nil
		}
		if err := p.Run(p.Root, "flux", args...); err != nil {
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

// StampImages sets newTag to the version for each of the component's
// own images in every kustomization.yaml under dir. The published
// manifests then name the images that were pushed with them, so a
// cluster that follows the deploy artifact updates its manifests and
// its images together. An image from another project, such as
// mosquitto, keeps the tag in its manifest.
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
			return fmt.Errorf("parsing %s: %w", path, err)
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
