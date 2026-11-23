package main

import (
	"errors"
	"fmt"
	"maps"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// The metadata of a published image: the OCI keys (title, version,
// revision, source, documentation, and so on) and the sh.liken.* keys
// of a pinned image. ci publish passes every key to docker buildx bake
// as a --set argument, and no Dockerfile writes one. A Dockerfile that
// wrote a label would make a build in a fork claim to be liken's, and
// labels set through bake do not change a layer that BuildKit caches.
//
// Each OCI key, except base.name and base.digest, is written twice: as
// a label in the image config, which docker inspect reads, and as an
// annotation on the image index and on each image manifest, because
// ghcr.io reads an index's description from the index's annotations.
// base.name and base.digest are written only as annotations.

const ociPrefix = "org.opencontainers.image."

func ociKey(name string) string { return ociPrefix + name }

const (
	vendor = "liken"
	// homePage is the value of the url key of every image.
	homePage = "https://liken.sh/"
	// upstreamLabel prefixes one label for each entry of
	// [package.upstream] in a pinned image's package.toml.
	upstreamLabel = "sh.liken.upstream."
	// descriptionLimit is how many characters of the description
	// ghcr.io shows.
	descriptionLimit = 512
)

// validateImage checks an image's description. The publish writes it
// into a label and two annotations, and ghcr.io shows it on the
// package page as one line of plain text of at most 512 characters.
// The check also rejects an em-dash, which brand/voice.md forbids.
func validateImage(path string, image Image) error {
	description := image.Description
	target := ociKey("description")
	switch {
	case strings.TrimSpace(description) == "":
		return fmt.Errorf("%s: the image %q has no description, and the publish writes it into %s, which ghcr.io shows on the package page", path, image.Name, target)
	case strings.ContainsAny(description, "\r\n"):
		return fmt.Errorf("%s: the description of the image %q is more than one line, and ghcr.io shows %s on the package page as one line", path, image.Name, target)
	case utf8.RuneCountInString(description) > descriptionLimit:
		return fmt.Errorf("%s: the description of the image %q is %d characters, and ghcr.io shows %d characters of %s on the package page", path, image.Name, utf8.RuneCountInString(description), descriptionLimit, target)
	case strings.Contains(description, "\u2014"):
		return fmt.Errorf("%s: the description of the image %q contains an em-dash, and brand/voice.md forbids em-dashes", path, image.Name)
	}
	return nil
}

// imageMetadata is what the publish writes on one image: the labels in
// its config, and the annotations on its index and on each image
// manifest. buildx writes no annotation on an attestation manifest.
type imageMetadata struct {
	Labels, Annotations map[string]string
}

// metadata returns the labels and annotations of one image at the
// version. recipe is the recipe hash of a pinned component, and "" for
// a tracked one.
//
// An image inherits every label of the image in its FROM line, and
// Docker cannot delete an inherited label. So the image writes each
// label key that any repository image in its FROM chain writes, with
// the value "" for a key it does not own. Otherwise an image built on
// mpv would carry mpv's upstream versions and recipe as if they were
// its own. Annotations are not inherited.
func (p Publisher) metadata(c *Component, image Image, version, recipe string) (imageMetadata, error) {
	m := imageMetadata{Labels: p.ownLabels(c, image, version, recipe), Annotations: map[string]string{}}
	for key, value := range m.Labels {
		if strings.HasPrefix(key, ociPrefix) {
			m.Annotations[key] = value
		}
	}
	inherited, err := p.inheritedLabels(c, image)
	if err != nil {
		return m, err
	}
	for _, key := range inherited {
		if _, own := m.Labels[key]; !own {
			m.Labels[key] = ""
		}
	}
	published, err := readOverride(filepath.Join(p.Root, overrideFile))
	if err != nil {
		return m, err
	}
	name, digest, err := p.baseReference(c, image, published)
	if err != nil {
		return m, err
	}
	if name != "" {
		m.Annotations[ociKey("base.name")] = name
		m.Annotations[ociKey("base.digest")] = digest
	}
	return m, nil
}

// ownLabels returns the labels that an image of the component sets to
// a value of its own.
//
// A tracked image gets licenses from its package.toml, because it
// contains liken's own code. A pinned image contains other projects'
// packages under their own licenses, so it gets no licenses label and
// its SBOM lists the license of each package. created is the
// committer time of the commit, so running the publish again at the
// same commit writes the same value. The config's own created field is
// the build time, because passing SOURCE_DATE_EPOCH to the build would
// change the cache key of each WORKDIR step and miss the layer cache.
func (p Publisher) ownLabels(c *Component, image Image, version, recipe string) map[string]string {
	labels := map[string]string{
		ociKey("title"):         image.Name,
		ociKey("description"):   image.Description,
		ociKey("version"):       version,
		ociKey("revision"):      p.Commit,
		ociKey("created"):       p.Created.UTC().Format(time.RFC3339),
		ociKey("source"):        source,
		ociKey("url"):           homePage,
		ociKey("documentation"): documentationURL(c, p.Commit),
		ociKey("vendor"):        vendor,
	}
	if !c.Pinned() && c.Package.License != "" {
		labels[ociKey("licenses")] = c.Package.License
	}
	if c.Pinned() {
		labels[recipeLabel] = recipe
		for name, upstream := range c.Package.Upstream {
			labels[upstreamLabel+name] = upstream
		}
	}
	return labels
}

// inheritedLabels lists every label key written by a repository image
// in the image's FROM chain, including keys that a base inherited from
// its own base. The walk stops at scratch, at an image from outside
// the repository, and at a base that the image declares as a
// directory context.
func (p Publisher) inheritedLabels(c *Component, image Image) ([]string, error) {
	producer := ImageProducers(p.Components)
	keys := map[string]bool{}
	seen := map[string]bool{}
	for {
		parsed, err := readDockerfile(filepath.Join(p.Root, c.Dir, orDefault(image.File, "Dockerfile")))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Name(), err)
		}
		base := parsed.FromImage(image.Target)
		if _, dir := image.Contexts[base]; dir || seen[base] {
			break
		}
		owner, ok := p.Components[producer[base]]
		if !ok {
			break
		}
		seen[base] = true
		i := slices.IndexFunc(owner.Outputs.Images, func(other Image) bool { return other.Name == base })
		c, image = owner, owner.Outputs.Images[i]
		for key := range p.ownLabels(c, image, "", "") {
			keys[key] = true
		}
	}
	return slices.Sorted(maps.Keys(keys)), nil
}

// baseReference returns the values of the OCI keys base.name and
// base.digest. For a pinned base that the override file points the
// build at, they are its tag and the digest of its index. For an
// outside image of toolchains/images.toml, they are its fully
// qualified name with its tag, when it has one, and its digest. For a base that the build makes from the
// tree, which was never published, and for scratch, both are empty.
func (p Publisher) baseReference(c *Component, image Image, published map[string]string) (name, digest string, err error) {
	parsed, err := readDockerfile(filepath.Join(p.Root, c.Dir, orDefault(image.File, "Dockerfile")))
	if err != nil {
		return "", "", fmt.Errorf("%s: %w", c.Name(), err)
	}
	base := parsed.FromImage(image.Target)
	target, err := newBakeTarget(p.Root, c, image, ImageProducers(p.Components), p.Components)
	if err != nil {
		return "", "", err
	}
	for _, context := range target.Contexts {
		if context.Name != base {
			continue
		}
		if context.Published != "" {
			name, digest, _ = strings.Cut(published[context.Published], "@")
			return name, digest, nil
		}
		if reference, ok := strings.CutPrefix(context.Value, "docker-image://"); ok {
			r := parseImageRef(reference)
			name = qualifiedName(r.Name)
			if r.Tag != "" {
				name += ":" + r.Tag
			}
			return name, r.Digest, nil
		}
	}
	return "", "", nil
}

// documentationURL returns the component's manual on the site, or, for
// a component with no manual, its directory in the repository at the
// commit.
func documentationURL(c *Component, commit string) string {
	switch {
	case c.Docs != nil && c.Docs.Prefix != "":
		return homePage + c.Docs.Prefix + "/"
	case c.Docs != nil:
		return homePage + "docs/"
	}
	return source + "/tree/" + commit + "/" + c.Dir
}

// bakeArgs returns the --set arguments that write the metadata on the
// bake target, in sorted order. Each annotation goes on the index and
// on the image manifest.
func (m imageMetadata) bakeArgs(target string) []string {
	var args []string
	for _, key := range sortedKeys(m.Labels) {
		args = append(args, "--set", target+".labels."+key+"="+m.Labels[key])
	}
	for _, key := range sortedKeys(m.Annotations) {
		args = append(args, "--set", target+".annotations=index,manifest:"+key+"="+m.Annotations[key])
	}
	return args
}

// labelInstructions lists each LABEL and MAINTAINER instruction of a
// Dockerfile. The publish writes every key of an image, so a key in a
// Dockerfile would be a second copy that can drift from package.toml,
// and a fork that builds the file would carry a label that claims the
// image is liken's. MAINTAINER writes the config's author field, not a
// label, and has the same problem.
func labelInstructions(file, text string) []string {
	var findings []string
	for _, l := range logicalLines(text) {
		words := strings.Fields(l.text)
		if len(words) == 0 {
			continue
		}
		switch instruction := strings.ToUpper(words[0]); instruction {
		case "LABEL", "MAINTAINER":
			findings = append(findings, fmt.Sprintf("%s:%d writes %s, and ci publish writes every label and annotation of an image from its package.toml", file, l.n, instruction))
		}
	}
	return findings
}

// deployAnnotations returns the --annotations arguments for the flux
// push of the component's deploy artifact. flux sets source and
// revision from its own flags, with the revision in the form
// <version>@sha1:<commit>. It sets created to the time of the push
// unless an annotation sets it, so created here is the commit's time,
// which is when the manifests were written. The manifests are
// liken's own files, so licenses is the component's license.
//
// flux splits each argument at "=" and refuses one with more than two
// parts, so a value that contains "=" fails here, in a dry run too.
func (p Publisher) deployAnnotations(c *Component, version string) ([]string, error) {
	values := map[string]string{
		ociKey("created"):       p.Created.UTC().Format(time.RFC3339),
		ociKey("title"):         c.DeployPackage(),
		ociKey("description"):   "The Kubernetes manifests of " + c.Name() + ", which name its images at the same version.",
		ociKey("version"):       version,
		ociKey("documentation"): documentationURL(c, p.Commit),
		ociKey("url"):           homePage,
		ociKey("vendor"):        vendor,
	}
	if c.Package.License != "" {
		values[ociKey("licenses")] = c.Package.License
	}
	var args []string
	for _, key := range sortedKeys(values) {
		if strings.Contains(values[key], "=") {
			return nil, fmt.Errorf("%s: the annotation %s=%q contains \"=\", and flux push artifact refuses an annotation with \"=\" in its value", c.Name(), key, values[key])
		}
		args = append(args, "--annotations", key+"="+values[key])
	}
	return args, nil
}

var errNoImage = errors.New("the registry has no image")

// ghcr.io can return 404 for a digest for a few seconds after it
// accepts the push, and it can answer 503 or send no answer before the
// client's timeout. So the publish reads the labels of a digest it
// just pushed up to pushedLabelTries times, and waits 3, 6, 9, and 12
// seconds between the reads.
const pushedLabelTries = 5

var pushedLabelPause = 3 * time.Second

// checkPushedLabels runs checkLabels on a digest that this run just
// pushed, and reads again only when readAgain says so.
func (p Publisher) checkPushedLabels(image Image, digest string, want map[string]string) error {
	for try := 1; ; try++ {
		err := p.checkLabels(image, digest, want)
		if err == nil || !readAgain(err) || try == pushedLabelTries {
			return err
		}
		time.Sleep(time.Duration(try) * pushedLabelPause)
	}
}

// readAgain reports whether a failed read of a pushed digest can
// succeed later: the registry did not serve the digest yet, answered
// 5xx, or did not answer before the client's timeout. Labels that
// differ, or a refused token, are the same on every read.
func readAgain(err error) bool {
	if errors.Is(err, errNoImage) || errors.Is(err, errRegistryUnavailable) {
		return true
	}
	var timeout net.Error
	return errors.As(err, &timeout) && timeout.Timeout()
}

// checkLabels reads the labels of the image in the registry at the
// reference, and fails when they differ from the labels that the
// publish wrote. A bump of toolchains/images.toml can give an outside
// base a label of its own, which no check of the tree reads, and the
// label would be inherited into an image that liken signs. The publish
// runs this before it signs, so it never signs an image with a label
// that it did not write.
func (p Publisher) checkLabels(image Image, reference string, want map[string]string) error {
	named := ref(image.Name, reference)
	if strings.HasPrefix(reference, "sha256:") {
		named = digestRef(image.Name, reference)
	}
	got, err := p.Registry.Labels(image.Name, reference)
	if err != nil {
		return fmt.Errorf("reading the labels of %s: %w", named, err)
	}
	if got == nil {
		return fmt.Errorf("%w at %s", errNoImage, named)
	}
	keys := map[string]bool{}
	for key := range got {
		keys[key] = true
	}
	for key := range want {
		keys[key] = true
	}
	var differ []string
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		value, pushed := got[key]
		wanted, written := want[key]
		switch {
		case pushed && !written:
			differ = append(differ, fmt.Sprintf("%s=%q, which the publish did not write", key, value))
		case !pushed && written:
			differ = append(differ, fmt.Sprintf("no %s, which the publish wrote as %q", key, wanted))
		case value != wanted:
			differ = append(differ, fmt.Sprintf("%s=%q, which the publish wrote as %q", key, value, wanted))
		}
	}
	if len(differ) > 0 {
		return fmt.Errorf("the labels of the pushed %s differ from the labels the publish wrote, so the publish does not sign it: %s", named, strings.Join(differ, "; "))
	}
	return nil
}
