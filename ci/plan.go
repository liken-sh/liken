package main

import (
	"fmt"
	"slices"
	"strings"
)

// An Event is the push, pull request, or dispatch that started the
// workflow run.
type Event struct {
	// Name is the GitHub event name: push, pull_request, or
	// workflow_dispatch.
	Name string
	// Ref is the full ref: refs/heads/main or refs/tags/2026.10.02-001.
	Ref string
	// Before is the commit the branch named before a push.
	Before string
	// Base is a pull request's base commit.
	Base string
	// Head is the commit the run builds.
	Head string
	// Verified is the head of the newest run on main that passed.
	// Unverified says why the plan could not find that run, such as an
	// error from the API or no run that passed yet. When both are "",
	// nothing looked for the run, as in a test or on a workstation.
	Verified, Unverified string
	// MainRef names main in the checkout, such as origin/main, for the
	// merge base of a branch that has no earlier commit to compare with.
	MainRef string
	// Publishing is true when the repository allows real publishes.
	// Without it, a push to main and a release tag run every check
	// and push nothing.
	Publishing bool
}

// Tag is the release tag the event pushed, or "".
func (e Event) Tag() string {
	if e.Name != "push" {
		return ""
	}
	tag, _ := strings.CutPrefix(e.Ref, "refs/tags/")
	if tag == e.Ref {
		return ""
	}
	return tag
}

// Main is true for a push to main.
func (e Event) Main() bool { return e.Name == "push" && e.Ref == "refs/heads/main" }

// Publish modes for a component's workflow.
const (
	publishNone    = "none"
	publishDev     = "dev"
	publishRelease = "release"
	// publishDry builds what a publish would push, and pushes nothing.
	publishDry = "dry"
)

// publishCode is the file that holds the code of every component's
// publish, other than the OS's.
const publishCode = "ci/publish.go"

// A Decision is what the run does with one component.
type Decision struct {
	// Check is true when the component's jobs run.
	Check bool `json:"check"`
	// Publish is none, dev, or release.
	Publish string `json:"publish"`
	// Version is the version the component's outputs get in this run.
	Version string `json:"version"`
	// Reason says why the component's jobs run, or why they do not.
	Reason string `json:"-"`
	// Changed is true when a change goes into the component's outputs,
	// so a release or a development build would publish it.
	Changed bool `json:"-"`
	// Why says what changed in the outputs.
	Why string `json:"-"`
	// Newest is the component's newest published release.
	Newest string `json:"-"`
	// Jobs and Images are the jobs and the images that run when Check
	// is true.
	Jobs   []string   `json:"jobs"`
	Images []ImageRun `json:"images"`
	// DryRun is true when the check stage runs the publish job in its
	// dry mode: its code changed, and the run does not publish.
	DryRun bool `json:"dryrun"`
}

// VersionsFunc lists the published versions of a component.
type VersionsFunc func(*Component) ([]string, error)

// Planner decides what a run does with every component.
type Planner struct {
	Components map[string]*Component
	Git        Git
	Versions   VersionsFunc
	// Recipes holds the recipe hash of each pinned component in the
	// tree, and PublishedRecipe reads the hash of its published tag.
	Recipes         map[string]string
	PublishedRecipe PublishedRecipeFunc
}

// Plan decides, for each component, whether its jobs run, whether it
// publishes, and under which version.
func (p Planner) Plan(e Event) (map[string]Decision, error) {
	if tag := e.Tag(); tag != "" {
		return p.release(e, tag)
	}
	return p.gate(e)
}

// release plans a release tag. A component releases when a change
// went into its outputs since its newest published release. The
// component's other jobs run with it, and a component that does not
// release runs nothing, because its commit passed the gate on the push
// before the tag.
func (p Planner) release(e Event, tag string) (map[string]Decision, error) {
	if err := CheckReleaseTag(tag); err != nil {
		return nil, err
	}
	decisions, err := p.Candidates(e.Head)
	if err != nil {
		return nil, err
	}
	for name, d := range decisions {
		if !p.Components[name].Pinned() {
			d.Version = tag
		}
		if d.Changed {
			d.Check = true
			d.Jobs, d.Images = everything(p.Components[name])
			if e.Publishing {
				d.Publish = publishRelease
			}
		}
		decisions[name] = d
	}
	return decisions, nil
}

// Candidates decides which components a release tag at the commit
// would publish, and why.
func (p Planner) Candidates(head string) (map[string]Decision, error) {
	published, err := p.pinnedPublished()
	if err != nil {
		return nil, err
	}
	decisions := map[string]Decision{}
	for _, c := range sortedComponents(p.Components) {
		if c.Pinned() {
			decisions[c.Name()] = pinnedDecision(c, published[c.Name()], false, "")
			continue
		}
		d := Decision{Publish: publishNone}
		if !c.HasOutputs() {
			d.Reason = "publishes nothing"
			decisions[c.Name()] = d
			continue
		}
		if err := p.sinceRelease(c, head, &d); err != nil {
			return nil, err
		}
		if d.Changed {
			d.Reason = "releases: " + d.Why
		} else {
			d.Reason = "no change since " + d.Newest
		}
		decisions[c.Name()] = d
	}
	return decisions, nil
}

// sinceRelease fills in whether a change went into the component's
// outputs after its newest published release.
//
// The diff starts at the git tag of that release: the bare version for
// a release made in this repository, or <component>/<version> for a
// release that a component made in its own repository before it moved
// here. The prefixed tag wins, because the OS's bare tags share the
// same calendar, and a bare tag of the same date names the OS's commit.
// A component with no release, or whose release has no git tag here,
// releases.
func (p Planner) sinceRelease(c *Component, head string, d *Decision) error {
	versions, err := p.Versions(c)
	if err != nil {
		return fmt.Errorf("%s: %w", c.Name(), err)
	}
	d.Newest = NewestRelease(versions)
	if d.Newest == "" {
		d.Changed, d.Why = true, "nothing is published yet"
		return nil
	}
	from := ""
	for _, tag := range []string{c.Name() + "/" + d.Newest, d.Newest} {
		if p.Git.HasTag(tag) {
			from = tag
			break
		}
	}
	if from == "" {
		d.Changed, d.Why = true, "no git tag names its release "+d.Newest
		return nil
	}
	diff, err := p.Git.ReadDiff(p.Components, from, head)
	if err != nil {
		return err
	}
	d.Changed, d.Why = ReleaseChanged(p.Components, c.Name(), diff)
	if d.Changed {
		d.Why += " (since " + from + ")"
	}
	return nil
}

// gate plans a push to a branch, a pull request, or a dispatch. The
// jobs of every component whose check paths changed run. A push to
// main also publishes a development build of each component whose
// outputs changed since its newest published version, and runs its jobs
// first.
func (p Planner) gate(e Event) (map[string]Decision, error) {
	list, all := p.Comparisons(e)
	diff, err := p.readDiffs(list, e.Head)
	if err != nil {
		return nil, err
	}
	var dev string
	if e.Main() {
		commit, err := p.Git.Commit(e.Head)
		if err != nil {
			return nil, err
		}
		tag, count, err := p.Git.Describe(commit)
		if err != nil {
			return nil, err
		}
		dev = DevVersion(tag, count, commit)
	}
	published, err := p.pinnedPublished()
	if err != nil {
		return nil, err
	}
	sel := selector{root: p.Git.Dir, components: p.Components, producer: ImageProducers(p.Components), diff: diff}
	decisions := map[string]Decision{}
	for _, c := range sortedComponents(p.Components) {
		d := Decision{Publish: publishNone, Version: dev}
		switch {
		case all != "":
			d.Check, d.Reason = true, "every job runs: "+all
			d.Changed, d.Why = true, d.Reason
		default:
			var file string
			if d.Check, file = CheckChanged(p.Components, c.Name(), diff); d.Check {
				d.Reason = "changed: " + file
			} else {
				d.Reason = "nothing in its paths changed"
			}
			d.Changed, d.Why = ReleaseChanged(p.Components, c.Name(), diff)
		}
		if c.Pinned() {
			d = pinnedDecision(c, published[c.Name()], d.Check, d.Reason)
		}
		// The OS publishes only releases: a machine installs from the
		// channel, and the channel holds releases.
		devBuild := e.Main() && c.HasOutputs() && !c.Outputs.Channel
		if devBuild && e.Publishing && !c.Pinned() && !d.Changed {
			if err := p.sincePublished(c, e.Head, &d); err != nil {
				return nil, fmt.Errorf("%s: %w", c.Name(), err)
			}
			if d.Changed && !d.Check {
				d.Check, d.Reason = true, "publishes: "+d.Why
			}
		}
		if devBuild && e.Publishing && d.Changed {
			d.Publish = publishDev
		}
		if d.Check {
			p.selectJobs(c, &d, sel, all != "")
		}
		if err := p.dryRun(c, &d, diff, all != "", e.Head); err != nil {
			return nil, err
		}
		decisions[c.Name()] = d
	}
	return decisions, nil
}

// selectJobs fills in the jobs and the images that run. A run that
// publishes the component, builds a pinned tag that is not published,
// or changed the component's workflow runs all of them, and so does a
// run with no commit to compare with. Otherwise the selector decides,
// and a component whose jobs and images all read nothing of the change
// does not run.
func (p Planner) selectJobs(c *Component, d *Decision, sel selector, all bool) {
	_, workflow := sel.diff.Workflows[c.Name()]
	if all || workflow || d.Publish != publishNone || c.Pinned() && d.Changed {
		d.Jobs, d.Images = everything(c)
		return
	}
	d.Jobs, d.Images = sel.Select(c)
	if len(d.Jobs) == 0 && len(d.Images) == 0 {
		d.Check, d.Reason = false, "no job or image reads the change; "+d.Reason
	}
}

// dryRun runs the component's publish job in its dry mode when the
// code of that publish changed and the run does not publish the
// component, and when the run has no commit to compare with. A dry run
// of a tracked component takes a development version, so the publish
// sees a version of the shape it sees on main.
func (p Planner) dryRun(c *Component, d *Decision, diff Diff, all bool, head string) error {
	changed := diff.Publishes[c.Name()]
	if slices.Contains(diff.Files, publishCode) && !c.Outputs.Channel {
		changed = publishCode
	}
	if all {
		changed = "every job runs"
	}
	if changed == "" || d.Publish != publishNone || !c.HasOutputs() {
		return nil
	}
	d.DryRun, d.Check = true, true
	d.Reason += "; a dry run of the publish, because of " + changed
	if d.Version != "" || c.Outputs.Channel {
		return nil
	}
	commit, err := p.Git.Commit(head)
	if err != nil {
		return err
	}
	tag, count, err := p.Git.Describe(commit)
	if err != nil {
		return err
	}
	d.Version = DevVersion(tag, count, commit)
	return nil
}
