package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// baseTree is a repository with a pinned base, base, that app builds
// on, and a pinned component, lone, that no image builds on.
func baseTree(t *testing.T) string {
	return writeTree(t, map[string]string{
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 2\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n",
		"base/Dockerfile":   "FROM scratch\n",
		"lone/package.toml": "[package]\nname = \"lone\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"lone\"\ndescription = \"The lone image.\"\n",
		"lone/Dockerfile":   "FROM scratch\n",
		"app/package.toml":  "[package]\nname = \"app\"\n[depends]\ncomponents = [\"base\"]\n[[outputs.images]]\nname = \"app\"\ndescription = \"The app image.\"\n",
		"app/Dockerfile":    "FROM base\n",
	})
}

// publishBase puts an image into the registry the way buildx pushes a
// pinned base: an index under its tag and under its digest, one amd64
// image, and a config whose recipe label is the recipe. It returns the
// reference that names the index by its digest.
func publishBase(fake *fakeRegistry, pkg, tag, recipe string) string {
	index := fmt.Sprintf(`{"manifests": [{"digest": "sha256:%s-image", "platform": {"os": "linux", "architecture": "amd64"}}]}`, pkg)
	digest := sha256Of(index)
	fake.manifests[pkg+"/"+tag] = index
	fake.manifests[pkg+"/"+digest] = index
	fake.manifests[pkg+"/sha256:"+pkg+"-image"] = fmt.Sprintf(`{"config": {"digest": "sha256:%s-config"}}`, pkg)
	fake.blobs["sha256:"+pkg+"-config"] = fmt.Sprintf(`{"config": {"Labels": {"sh.liken.recipe": %q}}}`, recipe)
	return "ghcr.io/liken-sh/" + pkg + ":" + tag + "@" + digest
}

func treeRecipe(t *testing.T, root, component string) string {
	t.Helper()
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	recipes, err := Recipes(root, components)
	if err != nil {
		t.Fatalf("computing the recipes: %v", err)
	}
	return recipes[component]
}

// A consumer pulls a pinned base only when the base's tag is on the
// registry, a workflow of its component signed it, and it was built
// from the tree's recipe. Every other base builds from the tree, and
// the override file gives the reason.
func TestAConsumerPullsAPinnedBaseOnlyWhenEveryCheckPasses(t *testing.T) {
	cases := []struct {
		name              string
		published, signed bool
		recipe            string
		want              []string
	}{
		{"pulled", true, true, "", []string{
			"# base: pulls ghcr.io/liken-sh/base:20260928-2@sha256:",
			"variable \"PUBLISHED_BASE\" {\n  default = \"ghcr.io/liken-sh/base:20260928-2@sha256:",
		}},
		{"not published", false, true, "", []string{
			"# base: builds from the tree",
		}},
		{"another recipe", true, true, "sha256:other", []string{
			"# base: builds from the tree: ghcr.io/liken-sh/base:20260928-2@sha256:",
			"was built from the recipe sha256:other",
		}},
		{"unsigned", true, false, "", []string{
			"# base: builds from the tree: no signature of",
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := baseTree(t)
			recipe := c.recipe
			if recipe == "" {
				recipe = treeRecipe(t, root, "base")
			}
			fake := &fakeRegistry{manifests: map[string]string{}, blobs: map[string]string{}}
			reference := publishBase(fake, "base", "20260928-2", recipe)
			if !c.published {
				fake = &fakeRegistry{}
			}
			w := newFakeEnv(t, fake)
			w.signed[reference] = c.signed
			if err := w.run([]string{"bases", "-root", root}); err != nil {
				t.Fatalf("running bases: %v", err)
			}
			text := readFile(t, filepath.Join(root, overrideFile))
			for _, want := range c.want {
				if !strings.Contains(text, want) {
					t.Errorf("the override file lacks %q, want it in:\n%s", want, text)
				}
			}
			if strings.Contains(text, "lone") {
				t.Errorf("the override file names lone, which no image builds on, want no such base:\n%s", text)
			}
			if pulled := strings.Contains(text, "variable \"PUBLISHED_BASE\""); pulled != (c.name == "pulled") {
				t.Errorf("PUBLISHED_BASE is set in the override file: %t, want the reverse:\n%s", pulled, text)
			}
		})
	}
}

// The signature must come from the workflow of the base's own
// component, run on main or on a release tag.
func TestABaseIsVerifiedAgainstItsOwnComponentsWorkflow(t *testing.T) {
	root := baseTree(t)
	fake := &fakeRegistry{manifests: map[string]string{}, blobs: map[string]string{}}
	reference := publishBase(fake, "base", "20260928-2", treeRecipe(t, root, "base"))
	w := newFakeEnv(t, fake)
	var identities []string
	w.VerifyImage = func(_, ref, identity string) error {
		identities = append(identities, ref+" "+identity)
		return nil
	}
	if err := w.run([]string{"bases", "-root", root}); err != nil {
		t.Fatalf("running bases: %v", err)
	}
	want := reference + ` ^https://github\.com/liken-sh/liken/\.github/workflows/component-base\.yaml@refs/(heads/main|tags/[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3})$`
	if len(identities) != 1 || identities[0] != want {
		t.Errorf("the verified images are %q, want only %q", identities, want)
	}
}

// In CI, the command also writes the override file to the step output
// bases, which each build reads, and lists each base in the job summary.
func TestCIGetsTheOverrideFileAsAnOutputAndASummary(t *testing.T) {
	root := baseTree(t)
	fake := &fakeRegistry{manifests: map[string]string{}, blobs: map[string]string{}}
	reference := publishBase(fake, "base", "20260928-2", treeRecipe(t, root, "base"))
	w := newFakeEnv(t, fake)
	w.signed[reference] = true
	dir := t.TempDir()
	w.vars["GITHUB_OUTPUT"] = filepath.Join(dir, "output")
	w.vars["GITHUB_STEP_SUMMARY"] = filepath.Join(dir, "summary")
	if err := w.run([]string{"bases", "-root", root}); err != nil {
		t.Fatalf("running bases: %v", err)
	}
	file := readFile(t, filepath.Join(root, overrideFile))
	if output := readFile(t, w.vars["GITHUB_OUTPUT"]); output != "bases<<"+outputDelimiter+"\n"+file+outputDelimiter+"\n" {
		t.Errorf("the output is\n%s\nwant the override file between the delimiters", output)
	}
	if summary := readFile(t, w.vars["GITHUB_STEP_SUMMARY"]); !strings.Contains(summary, "| `base` | pulls `"+reference+"` |") {
		t.Errorf("the summary lacks a row that pulls the base, in:\n%s", summary)
	}
}

// When the registry cannot be read, the base builds from the tree and
// the command still writes the file, so a registry outage slows a build
// and does not fail it.
func TestTheBaseBuildsFromTheTreeWhenTheRegistryCannotBeRead(t *testing.T) {
	cases := []struct {
		name   string
		damage func(*fakeRegistry, *fakeEnv)
		want   string
	}{
		{"unreachable", func(_ *fakeRegistry, w *fakeEnv) {
			w.Published.Registry = Registry{Base: "http://127.0.0.1:1", Owner: "liken-sh"}
		}, "# base: builds from the tree"},
		{"image missing", func(f *fakeRegistry, _ *fakeEnv) {
			delete(f.manifests, "base/sha256:base-image")
		}, "# base: builds from the tree"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := baseTree(t)
			fake := &fakeRegistry{manifests: map[string]string{}, blobs: map[string]string{}}
			reference := publishBase(fake, "base", "20260928-2", treeRecipe(t, root, "base"))
			w := newFakeEnv(t, fake)
			w.signed[reference] = true
			c.damage(fake, w)
			if err := w.run([]string{"bases", "-root", root}); err != nil {
				t.Fatalf("running bases: %v", err)
			}
			if text := readFile(t, filepath.Join(root, overrideFile)); !strings.Contains(text, c.want) || strings.Contains(text, "variable \"") {
				t.Errorf("the override file lacks %q or pulls the base, want it to build from the tree:\n%s", c.want, text)
			}
		})
	}
}

// -out writes the file elsewhere, such as into a job's temporary
// directory, and leaves the tree alone.
func TestTheOutFlagWritesTheOverrideFileOutsideTheTree(t *testing.T) {
	root := baseTree(t)
	w := newFakeEnv(t, &fakeRegistry{})
	out := filepath.Join(t.TempDir(), "bases.hcl")
	if err := w.run([]string{"bases", "-root", root, "-out", out}); err != nil {
		t.Fatalf("running bases: %v", err)
	}
	if text := readFile(t, out); !strings.Contains(text, "# base: builds from the tree") {
		t.Errorf("the file lacks the line that builds base from the tree:\n%s", text)
	}
	if _, err := os.Stat(filepath.Join(root, overrideFile)); !os.IsNotExist(err) {
		t.Errorf("stat of the override file in the tree returns %v, want that it does not exist", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}
