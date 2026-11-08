package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// overrideFile is the bake file that `ci bases` writes beside
// docker-bake.hcl. bake reads it after the generated file when no
// --file names another, and CI names both. git ignores it, because
// what it holds depends on the registry at the moment it was written.
const overrideFile = "docker-bake.override.hcl"

// outputDelimiter ends the override file in the step output of the
// plan job. The file is HCL and comments, which never hold it.
const outputDelimiter = "END_OF_PUBLISHED_BASES"

// githubIssuer is the OIDC issuer of a GitHub Actions job, which
// Fulcio writes into each certificate that it issues to a workflow.
const githubIssuer = "https://token.actions.githubusercontent.com"

// A PublishedBase is one image of a pinned component that another
// image of the repository builds on, and the result of its checks.
type PublishedBase struct {
	Image, Component, Tag string
	// Reference names the published image by its tag and digest when
	// every check passed, and is empty when the consumers build the
	// image from the tree.
	Reference string
	// Why says why the consumers build the image from the tree.
	Why string
}

// baseImages lists each image of a pinned component that a target of
// the bake file builds on, sorted by name.
func baseImages(root string, components map[string]*Component) ([]PublishedBase, error) {
	producer := ImageProducers(components)
	seen := map[string]bool{}
	var bases []PublishedBase
	for _, c := range sortedComponents(components) {
		for _, image := range c.Outputs.Images {
			t, err := newBakeTarget(root, c, image, producer, components)
			if err != nil {
				return nil, err
			}
			for _, context := range t.Contexts {
				base := strings.TrimPrefix(context.Value, "target:")
				if context.Published == "" || seen[base] {
					continue
				}
				seen[base] = true
				owner := components[producer[base]]
				bases = append(bases, PublishedBase{Image: base, Component: owner.Name(), Tag: owner.PinnedTag()})
			}
		}
	}
	slices.SortFunc(bases, func(a, b PublishedBase) int { return strings.Compare(a.Image, b.Image) })
	return bases, nil
}

// signerOf is the identity that must have signed a pinned component's
// image: the component's own workflow, at main or at a release tag,
// the two refs from which a publish runs.
func signerOf(component string) string {
	return `^https://github\.com/liken-sh/liken/\.github/workflows/component-` + regexp.QuoteMeta(component) +
		`\.yaml@refs/(heads/main|tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3})$`
}

// checkBase decides whether the consumers of a base pull its published
// image. They pull it only when three checks pass, and every check
// reads the image by its digest, so all three are about the same
// bytes:
//
//   - the tag is on the registry, and resolves to a digest;
//   - a signature of the component's own workflow covers the digest,
//     so CI built and pushed those bytes;
//   - the image's recipe label equals the recipe of the tree.
//
// The recipe covers every input of the build, so an image with the
// tree's recipe is what a build from the tree would make. The label is
// in the image's config, which the signed digest covers. A check that
// fails, or that cannot run, leaves the base to build from the tree,
// as every base built before this command existed.
func (w World) checkBase(root string, base PublishedBase, recipe string) PublishedBase {
	registry := w.Published.Registry
	tagged := ref(base.Image, base.Tag)
	digest, found, err := registry.Digest(base.Image, base.Tag)
	switch {
	case err != nil:
		base.Why = fmt.Sprintf("%s could not be read: %v", tagged, err)
		return base
	case !found:
		base.Why = tagged + " is not published"
		return base
	}
	reference := tagged + "@" + digest
	labels, err := registry.Labels(base.Image, digest)
	switch {
	case err != nil:
		base.Why = fmt.Sprintf("the labels of %s could not be read: %v", reference, err)
		return base
	case labels[recipeLabel] != recipe:
		base.Why = fmt.Sprintf("%s was built from the recipe %s, and the tree gives %s", reference, orDefault(labels[recipeLabel], "(none)"), recipe)
		return base
	}
	if err := w.VerifyImage(root, reference, signerOf(base.Component)); err != nil {
		base.Why = err.Error()
		return base
	}
	base.Reference = reference
	return base
}

// bases checks each pinned base, writes the override file, and, in a
// workflow, gives the file as the step output bases and lists each
// base in the job's summary. A base that fails a check builds from the
// tree, so the command fails only when it cannot read the repository
// or write the file.
func (w World) bases(root string, components map[string]*Component, out string) error {
	recipes, err := Recipes(root, components)
	if err != nil {
		return err
	}
	bases, err := baseImages(root, components)
	if err != nil {
		return err
	}
	for i, base := range bases {
		bases[i] = w.checkBase(root, base, recipes[base.Component])
	}
	file := overrideText(bases)
	if out == "" {
		out = filepath.Join(root, overrideFile)
	}
	if err := os.WriteFile(out, []byte(file), 0o644); err != nil {
		return err
	}
	for _, base := range bases {
		fmt.Fprintln(w.Stdout, baseLine(base))
	}
	if path := w.Getenv("GITHUB_OUTPUT"); path != "" {
		if err := appendFile(path, "bases<<"+outputDelimiter+"\n"+file+outputDelimiter+"\n"); err != nil {
			return err
		}
	}
	if path := w.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if err := appendFile(path, basesSummary(bases)); err != nil {
			return err
		}
	}
	return nil
}

func baseLine(base PublishedBase) string {
	if base.Reference != "" {
		return base.Image + ": pulls " + base.Reference
	}
	return base.Image + ": builds from the tree: " + base.Why
}

// overrideText is the override file: a comment for each base that
// says what its consumers do, and a variable for each base that they
// pull.
func overrideText(bases []PublishedBase) string {
	var b strings.Builder
	b.WriteString("# `make bases` wrote this file, from ci/bases.go. Do not edit it or\n")
	b.WriteString("# commit it. Each variable points the consumers of a pinned base at\n")
	b.WriteString("# its published image, which passed the checks that ci/bases.go\n")
	b.WriteString("# names. A base with no variable builds from the tree.\n#\n")
	for _, base := range bases {
		b.WriteString("# " + baseLine(base) + "\n")
	}
	for _, base := range bases {
		if base.Reference == "" {
			continue
		}
		fmt.Fprintf(&b, "\nvariable %q {\n  default = %q\n}\n", publishedVariable(base.Image), base.Reference)
	}
	return b.String()
}

// overrideVariable matches one variable that overrideText writes, and
// names it and its reference.
var overrideVariable = regexp.MustCompile(`(?m)^variable "([A-Z0-9_]+)" \{\n  default = "([^"]*)"\n\}$`)

// readOverride reads back the variables of the override file at path:
// the published reference that each pulled base's variable names. A
// missing file is an empty map, because then every base builds from
// the tree.
func readOverride(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	published := map[string]string{}
	for _, m := range overrideVariable.FindAllStringSubmatch(string(data), -1) {
		published[m[1]] = m[2]
	}
	return published, nil
}

func basesSummary(bases []PublishedBase) string {
	var b strings.Builder
	b.WriteString("## The pinned bases\n\n")
	b.WriteString("Each build of this run pulls a base whose published image passed its checks, and builds every other base from the tree.\n\n")
	b.WriteString("| Base | Builds |\n| --- | --- |\n")
	for _, base := range bases {
		if base.Reference != "" {
			fmt.Fprintf(&b, "| `%s` | pulls `%s` |\n", base.Image, base.Reference)
		} else {
			fmt.Fprintf(&b, "| `%s` | from the tree: %s |\n", base.Image, base.Why)
		}
	}
	return b.String()
}

// cosignVerify checks the signature of an image with the cosign of
// toolchains/images.toml, the program that signed it. cosign fetches
// the Sigstore trust root and reads the signature from the registry,
// so the check needs the network and Docker, the same as the build
// that follows it. It prints cosign's last line when the check fails.
func cosignVerify(root, reference, identity string) error {
	tools, err := readPublishTools(root)
	if err != nil {
		return err
	}
	out, err := exec.Command("docker", "run", "--rm", "-e", "HOME=/tmp", tools.Cosign, "verify",
		"--certificate-oidc-issuer", githubIssuer, "--certificate-identity-regexp", identity, reference).CombinedOutput()
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		return fmt.Errorf("cosign could not verify %s against %s: %s", reference, identity, lines[len(lines)-1])
	}
	return nil
}
