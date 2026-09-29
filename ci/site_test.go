package main

import (
	"testing"
)

// siteFixture is selectionFixture with lib, a component with no manual
// and a Go job that writes a coverage profile, and a root workflow with
// a site job.
func siteFixture(t *testing.T) (*repo, string) {
	t.Helper()
	r, _ := selectionFixture(t)
	r.write("lib/package.toml", "[package]\nname = \"lib\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\ncoverage = [\"coverage.out\"]\n")
	r.write("lib/lib.go", "package lib\n")
	r.write(rootWorkflowFile, "jobs:\n  site:\n    runs-on: ubuntu\n")
	return r, r.commit("add lib and the site job")
}

// The site serves every manual and each component's coverage report,
// so a push to main deploys it when a manual's job or a job that writes
// a coverage profile ran, or when the site's own build changed.
func TestThePushToMainDeploysTheSiteWhenWhatItServesChanged(t *testing.T) {
	cases := []struct {
		name  string
		file  string
		text  string
		event Event
		site  bool
	}{
		{"a page of a manual", "operator/docs/index.md", "an edit\n", Event{}, true},
		{"a file that a coverage job reads", "lib/lib.go", "package lib // changed\n", Event{}, true},
		{"the root Makefile, which builds the site", "Makefile", "site:\n", Event{}, true},
		{"the site job", rootWorkflowFile, "jobs:\n  site:\n    runs-on: ubuntu-latest\n", Event{}, true},
		{"a Rust file that no manual and no coverage job reads", "operator/ui/app.rs", "fn main() { }\n", Event{}, false},
		{"any change when every job runs", "operator/ui/app.rs", "fn main() { }\n", Event{Unverified: "the API answered 403"}, true},
		{"a push to a branch", "operator/docs/index.md", "an edit\n", Event{Ref: "refs/heads/topic"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, before := siteFixture(t)
			r.write(c.file, c.text)
			r.commit("the change")
			e := c.event
			e.Name, e.Before, e.Head, e.Publishing = "push", before, "HEAD", true
			if e.Ref == "" {
				e.Ref = "refs/heads/main"
			}
			p := r.planner(nil)
			decisions, err := p.Plan(e)
			if err != nil {
				t.Fatal(err)
			}
			site, why, err := p.Site(e, decisions)
			if err != nil {
				t.Fatal(err)
			}
			if site != c.site {
				t.Errorf("the site deploys: %v (%s), want %v", site, why, c.site)
			}
		})
	}
}
