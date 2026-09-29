package main

import (
	"reflect"
	"testing"
)

// selectionFixture is a repository with brand, a base image, and an
// operator with a Go job, a manual, and two images. The operator image
// builds on the base with the operator's directory as its context, less
// ui/ and local/. The ui image has ui/ as its context, reads the base's
// assets as a named context, and has a smoke check.
func selectionFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r := newRepo(t, map[string]string{
		"brand/package.toml":      "[package]\nname = \"brand\"\n",
		"brand/layouts/page.html": "<html>\n",
		"base/package.toml":       "[package]\nname = \"base\"\n[[outputs.images]]\nname = \"base\"\n",
		"base/Dockerfile":         "FROM scratch\nCOPY assets /assets\n",
		"base/assets/logo.svg":    "<svg/>\n",
		"operator/package.toml": `[package]
name = "operator"
[depends]
components = ["base"]
[docs]
prefix = "operator"
[[jobs]]
name = "go"
toolchain = "go"
run = "make test"
[[jobs]]
name = "docs"
toolchain = "hugo"
module = "docs"
run = "make test-docs"
[outputs]
deploy = "deploy"
[[outputs.images]]
name = "operator"
[[outputs.images]]
name = "operator-ui"
context = "ui"
file = "ui/Dockerfile"
contexts = { assets = "base/assets" }
smoke = "operator/smoke/ui.sh"
`,
		"operator/Dockerfile":                       "FROM base\nCOPY . /src\n",
		"operator/.dockerignore":                    "ui\nlocal\n",
		"operator/main.go":                          "package main\n",
		"operator/docs/index.md":                    "a manual\n",
		"operator/ui/Dockerfile":                    "FROM scratch\nCOPY --from=assets logo.svg /\nCOPY app.rs /\n",
		"operator/ui/app.rs":                        "fn main() {}\n",
		"operator/smoke/ui.sh":                      "true\n",
		"operator/local/notes.txt":                  "notes\n",
		".github/workflows/component-operator.yaml": "# generated\n",
		bakeFile: bakeFileText("check", map[string]string{"base": "linux/amd64", "operator": "linux/amd64"}) +
			"\ntarget \"operator-ui\" {\n  context = \"operator/ui\"\n}\n",
	})
	r.run("tag", "operator/2026.09.27-001")
	r.run("tag", "2026.09.28-001")
	return r, r.run("rev-parse", "HEAD")
}

func TestAChangeRunsTheJobsAndImagesThatReadIt(t *testing.T) {
	cases := []struct {
		name   string
		file   string
		text   string
		jobs   []string
		images []string
	}{
		{"its Go source runs its jobs and the image whose context holds it", "operator/main.go", "package main // changed\n",
			[]string{"go", "docs"}, []string{"operator"}},
		{"a file in one image's context runs that image alone", "operator/ui/app.rs", "fn main() { }\n",
			[]string{"go", "docs"}, []string{"operator-ui"}},
		{"its manual runs its jobs and no image", "operator/docs/index.md", "an edit\n",
			[]string{"go", "docs"}, nil},
		{"its test runs its jobs and no image", "operator/main_test.go", "package main\n",
			[]string{"go", "docs"}, nil},
		{"an ignored file runs its jobs and no image", "operator/local/notes.txt", "more notes\n",
			[]string{"go", "docs"}, nil},
		{"its ignore file runs the image that reads it", "operator/.dockerignore", "ui\n",
			[]string{"go", "docs"}, []string{"operator"}},
		{"a smoke check runs its image", "operator/smoke/ui.sh", "false\n",
			[]string{"go", "docs"}, []string{"operator-ui"}},
		{"a base's Dockerfile runs every image that builds on it", "base/Dockerfile", "FROM scratch\nCOPY assets /a\n",
			[]string{"go", "docs"}, []string{"operator"}},
		{"a file in a named context runs the image that reads it, and the images on the base", "base/assets/logo.svg", "<svg></svg>\n",
			[]string{"go", "docs"}, []string{"operator", "operator-ui"}},
		{"brand's theme runs the manual's job alone", "brand/layouts/page.html", "<html lang=en>\n",
			[]string{"docs"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, before := selectionFixture(t)
			r.write(c.file, c.text)
			r.commit("the change")
			d := planGate(t, r, nil, Event{Name: "push", Ref: "refs/heads/topic", Before: before, Head: "HEAD"})["operator"]
			if !d.Check || !reflect.DeepEqual(d.Jobs, c.jobs) || !reflect.DeepEqual(imageNames(d.Images), c.images) {
				t.Errorf("operator: check %v, jobs %v, images %v; want jobs %v, images %v", d.Check, d.Jobs, imageNames(d.Images), c.jobs, c.images)
			}
		})
	}
}

func TestAGeneratedFileRunsTheJobsAndImagesThatReadIt(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(r *repo)
		jobs   []string
		images []string
	}{
		{"its workflow runs every job and image", func(r *repo) {
			r.write(".github/workflows/component-operator.yaml", "# generated again\n")
		}, []string{"go", "docs"}, []string{"operator", "operator-ui"}},
		{"an image's bake target runs that image alone", func(r *repo) {
			r.write(bakeFile, bakeFileText("check", map[string]string{"base": "linux/amd64", "operator": "linux/amd64"})+
				"\ntarget \"operator-ui\" {\n  context = \"operator/ui\"\n  target = \"ui\"\n}\n")
		}, nil, []string{"operator-ui"}},
		{"a base's bake target runs every image that builds on it", func(r *repo) {
			r.write(bakeFile, bakeFileText("check", map[string]string{"base": "linux/arm64", "operator": "linux/amd64"})+
				"\ntarget \"operator-ui\" {\n  context = \"operator/ui\"\n}\n")
		}, nil, []string{"operator"}},
		{"the bake file's shared part runs every image", func(r *repo) {
			r.write(bakeFile, bakeFileText("other", map[string]string{"base": "linux/amd64", "operator": "linux/amd64"})+
				"\ntarget \"operator-ui\" {\n  context = \"operator/ui\"\n}\n")
		}, nil, []string{"operator", "operator-ui"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, before := selectionFixture(t)
			c.edit(r)
			r.commit("the change")
			d := planGate(t, r, nil, Event{Name: "push", Ref: "refs/heads/topic", Before: before, Head: "HEAD"})["operator"]
			if !d.Check || !reflect.DeepEqual(d.Jobs, c.jobs) || !reflect.DeepEqual(imageNames(d.Images), c.images) {
				t.Errorf("operator: check %v, jobs %v, images %v; want jobs %v, images %v", d.Check, d.Jobs, imageNames(d.Images), c.jobs, c.images)
			}
		})
	}
}

func TestARunThatPublishesRunsEveryJobAndImage(t *testing.T) {
	r, before := selectionFixture(t)
	r.write("operator/ui/app.rs", "fn main() { }\n")
	r.commit("change the ui")
	published := map[string][]string{"operator": {"2026.09.27-001"}}
	d := planGate(t, r, published, Event{Name: "push", Ref: "refs/heads/main", Before: before, Head: "HEAD", Publishing: true})["operator"]
	if d.Publish != publishDev || !reflect.DeepEqual(d.Jobs, []string{"go", "docs"}) || !reflect.DeepEqual(imageNames(d.Images), []string{"operator", "operator-ui"}) {
		t.Errorf("operator: %+v", d)
	}
	for _, run := range d.Images {
		if want := (ImageRun{Image: run.Image, Load: true, Smoke: map[string]string{"operator-ui": "operator/smoke/ui.sh"}[run.Image]}); run != want {
			t.Errorf("the image run %+v, want %+v", run, want)
		}
	}
}

func TestAChangeThatNoJobOrImageReadsRunsNothing(t *testing.T) {
	r, _ := selectionFixture(t)
	r.write("base/.dockerignore", "notes.txt\n")
	before := r.commit("ignore the base's notes")
	r.write("base/notes.txt", "a note\n")
	r.commit("a file that the base's image does not read")
	if d := planGate(t, r, nil, Event{Name: "push", Ref: "refs/heads/topic", Before: before, Head: "HEAD"})["base"]; d.Check || d.Jobs != nil || d.Images != nil {
		t.Errorf("base: %+v", d)
	}
}
