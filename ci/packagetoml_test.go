package main

import (
	"slices"
	"strings"
	"testing"
)

// The base's package.toml from planFixture, with its check's command and its
// excluded paths as given, so a test edits one part and keeps the rest.
func baseConfig(run, exclude string) string {
	return "[package]\nname = \"base\"\n[[jobs]]\nname = \"check\"\ntoolchain = \"go\"\nrun = \"" + run + "\"\n" +
		"[outputs]\nexclude = [" + exclude + "]\n[[outputs.images]]\nname = \"base\"\n"
}

// A release decision for each component, for the commits after the fixture's
// releases.
func decided(t *testing.T, r *repo) map[string]Decision {
	t.Helper()
	decisions, err := r.planner(fixturePublished).Candidates("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return decisions
}

// The fixture with the base's package.toml in the shape baseConfig writes,
// committed and released, so a test's own edit is the only change since.
func configFixture(t *testing.T) *repo {
	t.Helper()
	r := planFixture(t)
	r.write("base/.dockerignore", "notes/\n")
	r.write("base/notes/build.md", "how the base builds\n")
	r.write("base/package.toml", baseConfig("make test", ""))
	r.commit("the base excludes nothing")
	r.run("tag", "-f", "base/2026.09.27-001")
	r.run("tag", "-f", "operator/2026.09.27-001")
	return r
}

// The jobs of a package.toml run the component's checks, and the excluded
// paths name files no output is built from, so an edit to either changes no
// output, and releases neither the component nor the components that depend
// on it.
func TestAnEditToTheJobsOrTheExcludedPathsReleasesNothing(t *testing.T) {
	cases := map[string]string{
		"a job":            baseConfig("make check", ""),
		"an excluded path": baseConfig("make test", "\"notes/\""),
	}
	for name, config := range cases {
		r := configFixture(t)
		r.write("base/package.toml", config)
		r.commit("edit " + name)

		decisions := decided(t, r)
		for _, component := range []string{"base", "operator"} {
			if d := decisions[component]; d.Changed {
				t.Errorf("%s: %s releases: %+v", name, component, d)
			}
		}
	}
}

// An edit to any other part of a package.toml can change what a build makes,
// so it releases the component and every component that depends on it.
func TestAnEditToAnImageReleasesTheComponentAndItsUsers(t *testing.T) {
	r := configFixture(t)
	r.write("base/package.toml", baseConfig("make test", "")+"platforms = [\"linux/arm64\"]\n")
	r.commit("build the base for another platform")

	decisions := decided(t, r)
	if d := decisions["base"]; !d.Changed || !strings.HasPrefix(d.Why, "changed: base/package.toml") {
		t.Errorf("base: %+v", d)
	}
	if d := decisions["operator"]; !d.Changed || !strings.HasPrefix(d.Why, "its dependency base changed: base/package.toml") {
		t.Errorf("operator: %+v", d)
	}
}

// The jobs are what the checks run, so an edit to them still runs the
// component's own checks.
func TestAnEditToTheJobsStillRunsTheComponentsChecks(t *testing.T) {
	r, before := generatedFixture(t)
	r.write("base/package.toml", baseConfig("make check", ""))
	r.commit("edit the base's check")

	if got := checked(planPush(t, r, before)); !slices.Contains(got, "base") {
		t.Errorf("the checks that run: %v", got)
	}
}

// A package.toml with no outputs table that gains one only for its excluded
// paths, as brand's did, still changes no output.
func TestAnOutputsTableThatHoldsOnlyExcludedPathsReleasesNothing(t *testing.T) {
	r := planFixture(t)
	r.write("brand/voice.md", "the rules\n")
	r.commit("the voice rules")
	r.run("tag", "-f", "2026.09.28-001")
	r.write("brand/package.toml", "[package]\nname = \"brand\"\n[outputs]\nexclude = [\"voice.md\"]\n"+
		"[[jobs]]\nname = \"check\"\ntoolchain = \"go\"\nrun = \"make test\"\n")
	r.commit("brand's prose goes into no build")

	if d := decided(t, r)["liken"]; d.Changed {
		t.Errorf("liken releases: %+v", d)
	}
}
