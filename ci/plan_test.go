package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repo is a git repository for a test, with helpers that commit and
// tag.
type repo struct {
	t   *testing.T
	git Git
}

func newRepo(t *testing.T, files map[string]string) *repo {
	t.Helper()
	r := &repo{t: t, git: Git{Dir: writeTree(t, files)}}
	r.run("init", "--quiet", "--initial-branch=main")
	r.run("config", "user.email", "test@example.com")
	r.run("config", "user.name", "test")
	r.commit("the first commit")
	return r
}

func (r *repo) run(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.git.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repo) write(name, text string) {
	r.t.Helper()
	path := filepath.Join(r.git.Dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// commit commits everything and returns the commit's sha.
func (r *repo) commit(message string) string {
	r.t.Helper()
	r.run("add", "-A")
	r.run("commit", "--quiet", "--allow-empty", "-m", message)
	return r.run("rev-parse", "HEAD")
}

func (r *repo) planner(published map[string][]string) Planner {
	r.t.Helper()
	components, err := LoadComponents(r.git.Dir)
	if err != nil {
		r.t.Fatal(err)
	}
	return Planner{Components: components, Git: r.git, Versions: func(c *Component) ([]string, error) {
		return published[c.Name()], nil
	}}
}

// planFixture is a repository with an OS, an operator that builds on a
// base, the base, and brand. The OS's release 2026.09.28-001 and the
// operator's imported release operator/2026.09.27-001 are tagged.
func planFixture(t *testing.T) *repo {
	r := newRepo(t, map[string]string{
		"brand/package.toml":    "[package]\nname = \"brand\"\n",
		"base/package.toml":     "[package]\nname = \"base\"\n[[outputs.images]]\nname = \"base\"\n",
		"operator/package.toml": "[package]\nname = \"operator\"\n[depends]\ncomponents = [\"base\"]\n[docs]\nprefix = \"operator\"\n[outputs]\ndeploy = \"deploy\"\n[[outputs.images]]\nname = \"operator\"\n",
		"liken/package.toml":    "[package]\nname = \"liken\"\n[depends]\ncomponents = [\"brand\"]\n[outputs]\nchannel = true\n",
		"operator/main.go":      "package main\n",
		"base/Dockerfile":       "FROM scratch\n",
	})
	r.run("tag", "operator/2026.09.27-001")
	r.run("tag", "base/2026.09.27-001")
	r.commit("an OS release")
	r.run("tag", "2026.09.28-001")
	return r
}

var fixturePublished = map[string][]string{
	"operator": {"2026.09.27-001", "2026.09.26-001", "2026.09.27-001-dev-003-abcdef01", "latest"},
	"base":     {"2026.09.27-001"},
	"liken":    {"2026.09.28-001"},
}

func TestAReleaseTagPublishesWhatChangedSinceEachComponentsOwnRelease(t *testing.T) {
	r := planFixture(t)
	r.write("operator/main.go", "package main // changed\n")
	r.write("operator/docs/index.md", "a manual edit\n")
	r.commit("change the operator")
	r.run("tag", "2026.10.02-001")
	decisions, err := r.planner(fixturePublished).Plan(Event{Name: "push", Ref: "refs/tags/2026.10.02-001", Head: "HEAD", Publishing: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Decision{
		"operator": {Check: true, Publish: publishRelease, Version: "2026.10.02-001"},
		"base":     {Check: false, Publish: publishNone, Version: "2026.10.02-001"},
		"liken":    {Check: false, Publish: publishNone, Version: "2026.10.02-001"},
		"brand":    {Check: false, Publish: publishNone, Version: "2026.10.02-001"},
	}
	for name, w := range want {
		d := decisions[name]
		if d.Check != w.Check || d.Publish != w.Publish || d.Version != w.Version {
			t.Errorf("%s: %+v, want %+v", name, d, w)
		}
	}
	if why := decisions["operator"].Why; why != "changed: operator/main.go (since operator/2026.09.27-001)" {
		t.Errorf("the operator's reason is %q", why)
	}
}

func TestAChangeToADependencyReleasesTheDependent(t *testing.T) {
	r := planFixture(t)
	r.write("base/Dockerfile", "FROM scratch\nLABEL x=y\n")
	r.commit("change the base")
	decisions, err := r.planner(fixturePublished).Candidates("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if !decisions["base"].Changed || !decisions["operator"].Changed || decisions["liken"].Changed {
		t.Fatalf("decisions %+v", decisions)
	}
	if why := decisions["operator"].Why; !strings.HasPrefix(why, "its dependency base changed") {
		t.Errorf("the operator's reason is %q", why)
	}
}

func TestAComponentWithNoReleaseOrNoTagReleases(t *testing.T) {
	r := planFixture(t)
	published := map[string][]string{"operator": {"2026.09.20-001"}, "liken": {"2026.09.28-001"}}
	decisions, err := r.planner(published).Candidates("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if d := decisions["base"]; !d.Changed || d.Why != "nothing is published yet" {
		t.Errorf("base: %+v", d)
	}
	if d := decisions["operator"]; !d.Changed || d.Why != "no git tag names its release 2026.09.20-001" {
		t.Errorf("operator: %+v", d)
	}
	if d := decisions["liken"]; d.Changed {
		t.Errorf("liken: %+v", d)
	}
}

func TestATagThatIsNotAReleaseVersionIsRefused(t *testing.T) {
	r := planFixture(t)
	for _, tag := range []string{"v1.0.0", "2026.10.02-000", "2026.10.02-1"} {
		if _, err := r.planner(fixturePublished).Plan(Event{Name: "push", Ref: "refs/tags/" + tag, Head: "HEAD"}); err == nil {
			t.Errorf("the tag %s planned a release", tag)
		}
	}
}

func TestAReleaseTagPublishesNothingWhilePublishingIsOff(t *testing.T) {
	r := planFixture(t)
	decisions, err := r.planner(map[string][]string{}).Plan(Event{Name: "push", Ref: "refs/tags/2026.09.28-001", Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if d := decisions["operator"]; !d.Check || d.Publish != publishNone {
		t.Errorf("operator: %+v", d)
	}
}

func TestAPushToMainPublishesADevelopmentBuildOfWhatChanged(t *testing.T) {
	r := planFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("operator/main.go", "package main // changed\n")
	head := r.commit("change the operator")
	decisions, err := r.planner(fixturePublished).Plan(Event{Name: "push", Ref: "refs/heads/main", Before: before, Head: "HEAD", Publishing: true})
	if err != nil {
		t.Fatal(err)
	}
	if d := decisions["operator"]; !d.Check || d.Publish != publishDev || d.Version != "2026.09.28-001-dev-001-"+head[:8] {
		t.Errorf("operator: %+v", d)
	}
	if d := decisions["base"]; d.Check || d.Publish != publishNone {
		t.Errorf("base: %+v", d)
	}
}

func TestAPushToMainRunsAManualChangeAndPublishesNothing(t *testing.T) {
	r := planFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("operator/docs/index.md", "a manual edit\n")
	r.commit("edit the manual")
	decisions, err := r.planner(fixturePublished).Plan(Event{Name: "push", Ref: "refs/heads/main", Before: before, Head: "HEAD", Publishing: true})
	if err != nil {
		t.Fatal(err)
	}
	if d := decisions["operator"]; !d.Check || d.Publish != publishNone {
		t.Errorf("operator: %+v", d)
	}
}

func TestTheOSNeverPublishesADevelopmentBuild(t *testing.T) {
	r := planFixture(t)
	before := r.run("rev-parse", "HEAD")
	r.write("liken/init/main.go", "package main\n")
	r.commit("change the OS")
	decisions, err := r.planner(fixturePublished).Plan(Event{Name: "push", Ref: "refs/heads/main", Before: before, Head: "HEAD", Publishing: true})
	if err != nil {
		t.Fatal(err)
	}
	if d := decisions["liken"]; !d.Check || d.Publish != publishNone {
		t.Errorf("liken: %+v", d)
	}
}

func TestAPushWithNoEarlierCommitRunsEveryJob(t *testing.T) {
	r := planFixture(t)
	cases := map[string]Event{
		"a new branch":          {Name: "push", Ref: "refs/heads/topic", Before: "0000000000000000000000000000000000000000", Head: "HEAD"},
		"a force push":          {Name: "push", Ref: "refs/heads/topic", Before: "1111111111111111111111111111111111111111", Head: "HEAD"},
		"a dispatch":            {Name: "workflow_dispatch", Ref: "refs/heads/main", Head: "HEAD"},
		"a pull request's base": {Name: "pull_request", Ref: "refs/pull/1/merge", Base: "", Head: "HEAD"},
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			decisions, err := r.planner(fixturePublished).Plan(e)
			if err != nil {
				t.Fatal(err)
			}
			for component, d := range decisions {
				if !d.Check || d.Publish != publishNone {
					t.Errorf("%s: %+v", component, d)
				}
			}
		})
	}
}

func TestAPullRequestRunsWhatItChanged(t *testing.T) {
	r := planFixture(t)
	base := r.run("rev-parse", "HEAD")
	r.write("brand/voice.md", "a rule\n")
	r.commit("change brand")
	decisions, err := r.planner(fixturePublished).Plan(Event{Name: "pull_request", Ref: "refs/pull/1/merge", Base: base, Head: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	for name, runs := range map[string]bool{"brand": true, "liken": true, "operator": true, "base": false} {
		if decisions[name].Check != runs {
			t.Errorf("%s: %+v", name, decisions[name])
		}
	}
}
