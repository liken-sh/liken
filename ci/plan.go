package main

import (
	"fmt"
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
)

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
	files, err := p.Git.Changed(from, head)
	if err != nil {
		return err
	}
	d.Changed, d.Why = ReleaseChanged(p.Components, c.Name(), files)
	if d.Changed {
		d.Why += " (since " + from + ")"
	}
	return nil
}

// gate plans a push to a branch, a pull request, or a dispatch. The
// jobs of every component whose check paths changed run. A push to
// main also publishes a development build of each component whose
// outputs changed in the push.
func (p Planner) gate(e Event) (map[string]Decision, error) {
	var base string
	switch e.Name {
	case "push":
		base = e.Before
	case "pull_request":
		base = e.Base
	}
	var files []string
	all := !p.Git.HasCommit(base)
	if !all {
		var err error
		if files, err = p.Git.Changed(base, e.Head); err != nil {
			return nil, err
		}
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
	decisions := map[string]Decision{}
	for _, c := range sortedComponents(p.Components) {
		d := Decision{Publish: publishNone, Version: dev}
		switch {
		case all:
			d.Check, d.Reason = true, "every job runs: "+allReason(e)
			d.Changed, d.Why = true, d.Reason
		default:
			var file string
			if d.Check, file = CheckChanged(p.Components, c.Name(), files); d.Check {
				d.Reason = "changed: " + file
			} else {
				d.Reason = "nothing in its paths changed"
			}
			d.Changed, d.Why = ReleaseChanged(p.Components, c.Name(), files)
		}
		if c.Pinned() {
			d = pinnedDecision(c, published[c.Name()], d.Check, d.Reason)
		}
		// The OS publishes only releases: a machine installs from the
		// channel, and the channel holds releases.
		if e.Main() && e.Publishing && d.Changed && c.HasOutputs() && !c.Outputs.Channel {
			d.Publish = publishDev
		}
		decisions[c.Name()] = d
	}
	return decisions, nil
}

func allReason(e Event) string {
	switch e.Name {
	case "workflow_dispatch":
		return "a dispatch runs everything"
	case "pull_request":
		return "the pull request's base is missing"
	}
	return "the push has no earlier commit to compare with"
}
