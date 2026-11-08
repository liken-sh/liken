package main

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// What a published image says about itself: the OCI keys, which name
// the image, its version, its commit, and where to read about it, and
// the sh.liken.* keys of a pinned image. ci publish writes every key
// at publish time, through --set arguments to docker buildx bake, and
// no Dockerfile names one. So a build in a fork carries no label that
// says it is liken's, and a change to a key changes no layer that
// BuildKit caches.
//
// Each OCI key goes into the image config as a label, and into the
// image index and each image manifest as an annotation, because
// ghcr.io reads the description of an index from the index's
// annotations, and a tool such as docker inspect reads the config.

// ociPrefix starts each key of the OCI image specification's
// annotations.
const ociPrefix = "org.opencontainers.image."

func ociKey(name string) string { return ociPrefix + name }

const (
	// vendor is the organization that publishes every image.
	vendor = "liken"
	// homePage is the project's site, the url key of every image.
	homePage = "https://liken.sh/"
	// upstreamLabel prefixes the label that holds one upstream version
	// of a pinned image, from [package.upstream] in its package.toml.
	upstreamLabel = "sh.liken.upstream."
	// descriptionLimit is the length in characters that ghcr.io shows
	// of org.opencontainers.image.description.
	descriptionLimit = 512
)

// validateImage checks the description of an image. The publish
// writes it into a label and two annotations, and ghcr.io shows it on
// the package page as one line of plain text of at most 512
// characters. The em-dash is the one character that brand/voice.md
// forbids that a check can find.
func validateImage(path string, image Image) error {
	description := image.Description
	where := "; the publish writes it into " + ociKey("description") + ", which ghcr.io shows on the package page"
	switch {
	case strings.TrimSpace(description) == "":
		return fmt.Errorf("%s: the image %q has no description%s", path, image.Name, where)
	case strings.ContainsAny(description, "\r\n"):
		return fmt.Errorf("%s: the description of the image %q is more than one line%s as one line", path, image.Name, where)
	case utf8.RuneCountInString(description) > descriptionLimit:
		return fmt.Errorf("%s: the description of the image %q is %d characters%s, and it shows %d", path, image.Name, utf8.RuneCountInString(description), where, descriptionLimit)
	case strings.Contains(description, "\u2014"):
		return fmt.Errorf("%s: the description of the image %q holds an em-dash, which brand/voice.md forbids", path, image.Name)
	}
	return nil
}

// imageMetadata is what the publish writes on one image: the labels of
// its config, and the annotations of its index and of each image
// manifest. buildx writes no annotation on an attestation manifest.
type imageMetadata struct {
	Labels, Annotations map[string]string
}

// metadata gives the labels and the annotations of one image at the
// version. recipe is the recipe of a pinned component, and "" for a
// tracked one.
//
// A label of the image that a Dockerfile builds FROM passes into the
// new config, and Docker cannot delete it. So the image writes each
// label that an image of the repository in its FROM chain writes, and
// a label it does not own takes the value "". Without that, a player
// built on mpv would say it holds mpv's upstream versions and mpv's
// recipe. Annotations do not pass through FROM.
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

// ownLabels gives the labels that an image of the component writes
// with a value of its own.
//
// licenses is the license of liken's own code in a tracked image, the
// license of its package.toml. A pinned image holds the packages of
// other projects under their own licenses, so it writes no licenses
// key, and its SBOM names the license of each package. created is the
// committer time of the commit, the same time the build takes as
// SOURCE_DATE_EPOCH, so the label and the config's created field agree,
// and a run again at the commit writes the same value.
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

// inheritedLabels lists every label key that an image of the
// repository in the image's FROM chain writes, through the whole
// chain, so a key that a base itself inherited counts too. The chain
// ends at scratch, at an image from outside the repository, and at a
// name that the image declares as a directory context.
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

// baseReference names the image that the build starts from, as the
// OCI keys base.name and base.digest state it: a pinned base that the
// override file points the build at, by its tag and the digest of its
// index, or an outside image of toolchains/images.toml, by its fully
// qualified name and its digest. A base that the build makes from the
// tree was never published, and scratch is no image, so neither has a
// reference.
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

// documentationURL is where a person reads about the component's
// images: its manual on the site, or, for a component with no manual,
// its directory in the repository at the commit, whose README.md says
// what it is.
func documentationURL(c *Component, commit string) string {
	switch {
	case c.Docs != nil && c.Docs.Prefix != "":
		return homePage + c.Docs.Prefix + "/"
	case c.Docs != nil:
		return homePage + "docs/"
	}
	return source + "/tree/" + commit + "/" + c.Dir
}

// bakeArgs gives the --set arguments that write the metadata on the
// target, in sorted order. Each annotation goes on the index and on the
// image manifest.
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
// Dockerfile would be a second copy that drifts from package.toml, and
// a fork that builds the file would carry a label that says the image
// is liken's. MAINTAINER writes the config's author field, not a
// label, and has the same fault.
func labelInstructions(file, text string) []string {
	var findings []string
	for _, l := range logicalLines(text) {
		words := strings.Fields(l.text)
		if len(words) == 0 {
			continue
		}
		switch instruction := strings.ToUpper(words[0]); instruction {
		case "LABEL", "MAINTAINER":
			findings = append(findings, fmt.Sprintf("%s:%d writes %s; ci publish writes every label and annotation of an image from its package.toml", file, l.n, instruction))
		}
	}
	return findings
}

// deployAnnotations gives the --annotations arguments of the flux push
// of the component's deploy artifact. flux writes source and revision
// from their own flags, with the revision in its <version>@sha1:<commit>
// form, and writes the time of the push as created unless an
// annotation gives it. The given time is the commit's, so a person
// reads when the manifests were written. The manifests are liken's own
// files, so licenses is the component's license.
//
// flux splits each argument at "=" and refuses one with more than two
// parts, so a value that holds "=" fails here, in a dry run too.
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
			return nil, fmt.Errorf("%s: the annotation %s=%q holds \"=\", which flux push artifact refuses", c.Name(), key, values[key])
		}
		args = append(args, "--annotations", key+"="+values[key])
	}
	return args, nil
}

// checkLabels reads the labels of the image that the registry serves
// at the reference, and fails when they differ from the labels that
// the publish wrote. An outside base can start to carry a label of its
// own after a bump of toolchains/images.toml, which no check of the
// tree sees, and the label would pass into the image under liken's
// signature. The publish runs this before it signs, so an image with a
// label that no one wrote is never signed.
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
		return fmt.Errorf("the registry serves no image for %s, so its labels cannot be checked", named)
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
		return fmt.Errorf("the pushed %s holds labels that differ from the ones the publish wrote, so it is not signed: %s", named, strings.Join(differ, "; "))
	}
	return nil
}
