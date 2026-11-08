package main

import (
	"reflect"
	"strings"
	"testing"
)

// Every image states one line that says what it is, because the
// publish writes it into org.opencontainers.image.description, which
// ghcr.io shows on the package page and allows 512 characters of.
func TestAnImageNeedsADescriptionOfOneLine(t *testing.T) {
	image := func(description string) string {
		return "[package]\nname = \"a\"\n[[outputs.images]]\nname = \"a\"\n" + description
	}
	cases := []struct {
		name, toml, wants string
	}{
		{"no description", image(""), "the image \"a\" has no description"},
		{"an empty description", image("description = \"\"\n"), "the image \"a\" has no description"},
		{"two lines", image("description = \"one\\ntwo\"\n"), "the description of the image \"a\" is more than one line"},
		{"513 characters", image("description = \"" + strings.Repeat("x", 513) + "\"\n"), "the description of the image \"a\" is 513 characters"},
		{"an em-dash", image("description = \"one \u2014 two\"\n"), "the description of the image \"a\" holds an em-dash"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := writeTree(t, map[string]string{"a/package.toml": c.toml})
			_, err := LoadComponents(root)
			if err == nil || !strings.Contains(err.Error(), c.wants) {
				t.Fatalf("got %v, want an error with %q", err, c.wants)
			}
		})
	}
}

func TestADescriptionOf512CharactersLoads(t *testing.T) {
	description := strings.Repeat("é", 512)
	root := writeTree(t, map[string]string{
		"a/package.toml": "[package]\nname = \"a\"\n[[outputs.images]]\nname = \"a\"\ndescription = \"" + description + "\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := components["a"].Outputs.Images[0].Description; got != description {
		t.Errorf("the description %q", got)
	}
}

// The bake file gives each target its image's description, so
// `docker buildx bake --list=targets` prints it. The description is
// plain text, so the bake file quotes it as an HCL string, in which a
// quote, a backslash, and the start of an interpolation are escaped.
func TestTheBakeFileDescribesEachTarget(t *testing.T) {
	root := writeTree(t, map[string]string{
		"a/package.toml": "[package]\nname = \"a\"\n[[outputs.images]]\nname = \"a\"\ndescription = 'The \"a\" image, with ${HOME}, %{x}, and a \\ in it.'\n",
		"a/Dockerfile":   "FROM scratch\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	bake, err := Bake(root, components)
	if err != nil {
		t.Fatal(err)
	}
	want := "target \"a\" {\n  description = \"The \\\"a\\\" image, with $${HOME}, %%{x}, and a \\\\ in it.\"\n"
	if !strings.Contains(string(bake), want) {
		t.Errorf("the bake file has no\n%s\nin:\n%s", want, bake)
	}
}

// A change to an image's description changes what the publish writes
// on the image, so it releases the component, and a push to main
// publishes a development build of it. The description is in the
// image's bake target, so the images job builds that image and runs its
// smoke check. The operator's build context leaves out package.toml,
// so the change reaches no other image of the component.
func TestAChangeToADescriptionPublishesTheComponentAndRunsTheImage(t *testing.T) {
	describe := func(ui string) string {
		return "[package]\nname = \"operator\"\n[[outputs.images]]\nname = \"operator\"\ndescription = \"The operator.\"\n" +
			"[[outputs.images]]\nname = \"operator-ui\"\ndescription = \"" + ui + "\"\ncontext = \"ui\"\nfile = \"ui/Dockerfile\"\n"
	}
	r := newRepo(t, map[string]string{
		"operator/package.toml":  describe("The ui."),
		"operator/Dockerfile":    "FROM scratch\nCOPY main.go /\n",
		"operator/.dockerignore": "ui\npackage.toml\n",
		"operator/main.go":       "package main\n",
		"operator/ui/Dockerfile": "FROM scratch\n",
	})
	generateBake := func() {
		t.Helper()
		components, err := LoadComponents(r.git.Dir)
		if err != nil {
			t.Fatal(err)
		}
		bake, err := Bake(r.git.Dir, components)
		if err != nil {
			t.Fatal(err)
		}
		r.write(bakeFile, string(bake))
	}
	generateBake()
	r.commit("the bake file")
	r.run("tag", "2026.09.27-001")
	before := r.run("rev-parse", "HEAD")
	r.write("operator/package.toml", describe("The media browser."))
	generateBake()
	r.commit("describe the ui")
	r.run("tag", "2026.10.02-001")
	published := map[string][]string{"operator": {"2026.09.27-001"}}
	cases := []struct {
		name  string
		event Event
		mode  string
	}{
		{"a push to main", Event{Name: "push", Ref: "refs/heads/main", Before: before, Head: "HEAD", Publishing: true}, publishDev},
		{"a release tag", Event{Name: "push", Ref: "refs/tags/2026.10.02-001", Head: "HEAD", Verified: before, Publishing: true}, publishRelease},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := planGate(t, r, published, c.event)["operator"]
			if d.Publish != c.mode || !d.Check || !reflect.DeepEqual(imageNames(d.Images), []string{"operator-ui"}) {
				t.Errorf("operator: publish %s, check %v, images %v; want %s and the image operator-ui", d.Publish, d.Check, imageNames(d.Images), c.mode)
			}
		})
	}
}
