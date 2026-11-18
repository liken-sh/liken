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
// docker-bake.hcl. bake reads it after the generated file when no -f
// flag selects other files, and CI passes both files with -f. git
// ignores it, because its content depends on the images that were in
// the registry when `ci bases` ran.
const overrideFile = "docker-bake.override.hcl"

// outputDelimiter is the heredoc delimiter of the multi-line step
// output `bases` that the plan job writes to $GITHUB_OUTPUT. The
// override file has only HCL variables and comments, so this string
// never appears in it.
const outputDelimiter = "END_OF_PUBLISHED_BASES"

// githubIssuer is the OIDC issuer of a GitHub Actions job, which
// Fulcio writes into each certificate that it issues to a workflow.
const githubIssuer = "https://token.actions.githubusercontent.com"

// A PublishedBase is one image of a pinned component that another
// image of the repository builds on, and the result of its checks.
type PublishedBase struct {
	Image, Component, Tag string
	// Reference is the published image's tag and digest when every
	// check passed. It is empty when the consumers build the image from
	// the tree.
	Reference string
	// Why is the reason the consumers build the image from the tree.
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

// signerOf returns a regular expression for the certificate identity
// that must have signed a pinned component's image: the component's own
// workflow, run on main or on a release tag, because a publish runs only
// on those refs.
func signerOf(component string) string {
	return `^https://github\.com/liken-sh/liken/\.github/workflows/component-` + regexp.QuoteMeta(component) +
		`\.yaml@refs/(heads/main|tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3})$`
}

// checkBase checks whether the consumers of a base pull its published
// image. They pull it only when the tag resolves to a digest on the
// registry, the image's recipe label equals the tree's recipe, and a
// signature of the component's own workflow covers the digest. The
// label and the signature are both read through the digest, so they
// apply to the same bytes. The recipe covers every input of the build,
// so an image with the tree's recipe is what a build from the tree
// would make. When a check fails or cannot run, the consumers build the
// base from the tree.
func (e Env) checkBase(root string, base PublishedBase, recipe string) PublishedBase {
	registry := e.Published.Registry
	tagged := ref(base.Image, base.Tag)
	digest, found, err := registry.Digest(base.Image, base.Tag)
	switch {
	case err != nil:
		base.Why = fmt.Sprintf("reading the tag %s: %v", tagged, err)
		return base
	case !found:
		base.Why = "the registry has no tag " + tagged
		return base
	}
	reference := tagged + "@" + digest
	labels, err := registry.Labels(base.Image, digest)
	switch {
	case err != nil:
		base.Why = fmt.Sprintf("reading the labels of %s: %v", reference, err)
		return base
	case labels[recipeLabel] != recipe:
		base.Why = fmt.Sprintf("%s was built from the recipe %s, and the tree has the recipe %s", reference, orDefault(labels[recipeLabel], "(none)"), recipe)
		return base
	}
	if err := e.VerifyImage(root, reference, signerOf(base.Component)); err != nil {
		base.Why = err.Error()
		return base
	}
	base.Reference = reference
	return base
}

// bases checks each pinned base and writes the override file to out,
// or to overrideFile at root when out is "". In a workflow it also
// writes the file to the step output bases and lists each base in the
// job summary. A base that fails a check builds from the tree, so the
// command fails only when it cannot read the repository or write a
// file.
func (e Env) bases(root string, components map[string]*Component, out string) error {
	recipes, err := Recipes(root, components)
	if err != nil {
		return err
	}
	bases, err := baseImages(root, components)
	if err != nil {
		return err
	}
	for i, base := range bases {
		bases[i] = e.checkBase(root, base, recipes[base.Component])
	}
	file := overrideText(bases)
	if out == "" {
		out = filepath.Join(root, overrideFile)
	}
	if err := os.WriteFile(out, []byte(file), 0o644); err != nil {
		return err
	}
	for _, base := range bases {
		fmt.Fprintln(e.Stdout, baseLine(base))
	}
	if path := e.Getenv("GITHUB_OUTPUT"); path != "" {
		if err := appendFile(path, "bases<<"+outputDelimiter+"\n"+file+outputDelimiter+"\n"); err != nil {
			return err
		}
	}
	if path := e.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
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

// overrideText returns the override file: a comment for each base that
// says whether its consumers pull the published image or build from
// the tree, and a variable for each base that they pull.
func overrideText(bases []PublishedBase) string {
	var b strings.Builder
	b.WriteString("# `make bases` writes this file with `ci bases` (ci/bases.go).\n")
	b.WriteString("# Do not edit it or commit it. Each variable points the consumers of a\n")
	b.WriteString("# pinned base at its published image, which passed the checks in\n")
	b.WriteString("# ci/bases.go. A base with no variable builds from the tree.\n#\n")
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
// captures its name and its reference.
var overrideVariable = regexp.MustCompile(`(?m)^variable "([A-Z0-9_]+)" \{\n  default = "([^"]*)"\n\}$`)

// readOverride returns the published reference of each base in the
// override file at path, by variable name. A missing file returns an
// empty map, so every base builds from the tree.
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

// cosignVerify checks the signature of an image with the cosign image
// pinned in toolchains/images.toml, the same one that the publish signs
// with. cosign downloads the Sigstore trust root and reads the signature
// from the registry, so the check needs the network and Docker, as the
// build that follows it does. The returned error includes the last
// line of cosign's output.
func cosignVerify(root, reference, identity string) error {
	tools, err := readPublishTools(root)
	if err != nil {
		return err
	}
	out, err := exec.Command("docker", "run", "--rm", "-e", "HOME=/tmp", tools.Cosign, "verify",
		"--certificate-oidc-issuer", githubIssuer, "--certificate-identity-regexp", identity, reference).CombinedOutput()
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		return fmt.Errorf("cosign rejected %s for the signer identity %s: %s", reference, identity, lines[len(lines)-1])
	}
	return nil
}
