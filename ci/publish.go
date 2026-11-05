package main

import (
	"fmt"
	"io/fs"
	"maps"
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
	// Components is every component, because a pinned component's
	// recipe covers the tags of its dependencies.
	Components map[string]*Component
	// SARIF is the directory where the Trivy scan of each pushed image
	// is written, or "" for no scan. See scan.
	SARIF string
	// DockerConfig is the directory of the Docker configuration that
	// holds the registry credential, which cosign pushes each signature
	// with. See sign.
	DockerConfig string
}

// Publish pushes the component's outputs under the version: every
// image first, then the deploy artifact, because a cluster follows the
// deploy artifact and must never read one that names an image that is
// not pushed yet.
//
// Each image carries an SBOM and its provenance, and cosign signs it,
// so a person who pulls it can check what it holds, how it was built,
// and that this repository's CI pushed it. See image.
//
// A version that is already published stays as it is. A run of the
// same tag again, after a failure, pushes only what the failed run did
// not.
//
// A release also moves each :latest tag, unless a newer release
// already has it. A development build never moves :latest.
//
// A dry run builds each image as a publish would, with the same
// arguments, labels, and tags, and stamps the deploy artifact, but it
// pushes nothing and moves no tag. It proves the publish code on a
// change to it, before a release needs it.
//
// A pinned component publishes its own tag in any mode, with its
// recipe hash as a label, and then moves :latest to it. A consumer in
// the repository builds on a base by its digest or from the tree, so
// no build reads :latest, but a person who pulls a base by hand reads
// it. Publish
// takes only the tree's own pin, and the plan publishes a pin only
// once, so the newest pin is the one that holds :latest.
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
			return fmt.Errorf("%q is not a development version", version)
		}
	case publishDry:
		if !devVersion.MatchString(version) && CheckReleaseTag(version) != nil {
			return fmt.Errorf("%q is not a version that a publish takes", version)
		}
	default:
		return fmt.Errorf("the publish mode %q is not dev, release, or dry", mode)
	}
	if c.Outputs.Channel {
		return fmt.Errorf("%s publishes to the release channel through its own workflow", c.Name())
	}
	for _, image := range c.Outputs.Images {
		if err := p.image(c, image, version, mode, nil); err != nil {
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
		return fmt.Errorf("the publish mode %q is not dev, release, or dry", mode)
	}
	if version != c.PinnedTag() {
		return fmt.Errorf("%s is pinned at %s, not %s", c.Name(), c.PinnedTag(), version)
	}
	recipe, err := Recipe(p.Root, p.Components, c)
	if err != nil {
		return err
	}
	labels := map[string]string{recipeLabel: recipe}
	for name, upstream := range c.Package.Upstream {
		labels[upstreamLabel+name] = upstream
	}
	for _, image := range c.Outputs.Images {
		if err := p.image(c, image, version, mode, labels); err != nil {
			return err
		}
	}
	return nil
}

// upstreamLabel prefixes the label that holds one upstream version of a
// pinned image, from [package.upstream] in its package.toml.
const upstreamLabel = "sh.liken.upstream."

func ref(name, tag string) string { return "ghcr.io/liken-sh/" + name + ":" + tag }

func digestRef(name, digest string) string { return "ghcr.io/liken-sh/" + name + "@" + digest }

// image builds and pushes one image through the bake file, so an
// image that builds on another image of the repository builds on the
// one at this commit. The bake file gives the target its context, its
// platforms, and its layer cache. Only the target that the command
// names is pushed; a base that the build needs builds with it and
// stays in the builder.
//
// The push carries an SBOM and the provenance, and cosign signs the
// pushed digest under each name of the image before anything else
// names it: before the scan, the move of :latest, and the deploy
// artifact. A signature that fails fails the publish. A run again then
// finds the version published, and signs it by its tag, which cosign
// resolves to the digest. A published version tag never moves, so the
// tag names the bytes that the failed run pushed.
func (p Publisher) image(c *Component, image Image, version, mode string, labels map[string]string) error {
	tools, err := readPublishTools(p.Root)
	if err != nil {
		return err
	}
	tags, err := p.Registry.Tags(image.Name)
	if err != nil {
		return err
	}
	names := append([]string{image.Name}, image.Aliases...)
	dry := mode == publishDry
	if slices.Contains(tags, version) && dry && c.Pinned() {
		fmt.Printf("%s is already published, so a publish would only sign it again, and the dry run builds nothing\n", ref(image.Name, version))
		return nil
	}
	if slices.Contains(tags, version) && !dry {
		fmt.Printf("%s is already published; a published version never changes\n", ref(image.Name, version))
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
		// The override file points the build at the published image of
		// each pinned base that passed its checks. With no file, every
		// base builds from the tree.
		if _, err := os.Stat(filepath.Join(p.Root, overrideFile)); err == nil {
			args = append(args, "--file", overrideFile)
		}
		if !dry {
			args = append(args, "--push")
		}
		if !c.Pinned() {
			args = append(args, set("args.VERSION", version)...)
		}
		all := map[string]string{
			"org.opencontainers.image.source":   source,
			"org.opencontainers.image.revision": p.Commit,
			"org.opencontainers.image.version":  version,
		}
		maps.Copy(all, labels)
		for _, key := range sortedKeys(all) {
			args = append(args, set("labels."+key, all[key])...)
		}
		for _, name := range names {
			args = append(args, set("tags", ref(name, version))...)
		}
		metadata, err := os.CreateTemp("", image.Name+"-metadata-*.json")
		if err != nil {
			return err
		}
		metadata.Close()
		defer os.Remove(metadata.Name())
		args = append(args, tools.attestArgs(target)...)
		args = append(args, "--metadata-file", metadata.Name(), target)
		if err := p.Run(p.Root, "docker", args...); err != nil {
			return fmt.Errorf("pushing %s: %w", ref(image.Name, version), err)
		}
		if !dry {
			digest, err := pushedDigest(metadata.Name(), target)
			if err != nil {
				return fmt.Errorf("pushing %s: %w", ref(image.Name, version), err)
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

// scan runs Trivy on the image that the publish just pushed. The scan
// runs here because only the publish knows which images it pushed: an
// image that is already published is not pushed again, and is not
// scanned again. scanImage gives the arguments of the scan and why a
// scan that fails does not fail the publish.
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
		if mode == publishDry {
			fmt.Printf("a publish would push %s from %s\n", artifact, dir)
			return nil
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
