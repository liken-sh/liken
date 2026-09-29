package main

import (
	"fmt"
	"slices"
	"strings"
)

// PublishedRecipeFunc reads the recipe label of the image that a
// pinned component publishes under its tag. found is false when the
// tag is not published.
type PublishedRecipeFunc func(*Component) (recipe string, found bool, err error)

// Recipe reads the recipe label of the component's first image at its
// pinned tag.
func (p Published) Recipe(c *Component) (string, bool, error) {
	labels, err := p.Registry.Labels(c.Outputs.Images[0].Name, c.PinnedTag())
	if err != nil || labels == nil {
		return "", false, err
	}
	return labels[recipeLabel], true, nil
}

// pinnedPublished finds which pinned components have their tag
// published already. A published tag whose recipe differs from the
// tree's fails the plan: a published tag never changes, so a change to
// the recipe needs a new revision. The error names every component
// that needs one.
func (p Planner) pinnedPublished() (map[string]bool, error) {
	published := map[string]bool{}
	var stale []string
	for _, c := range sortedComponents(p.Components) {
		if !c.Pinned() {
			continue
		}
		recipe, found, err := p.PublishedRecipe(c)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Name(), err)
		}
		published[c.Name()] = found
		if !found || recipe == p.Recipes[c.Name()] {
			continue
		}
		line := fmt.Sprintf("%s: %s was built from the recipe %q, and the tree gives %q. Raise revision in %s/package.toml",
			c.Name(), ref(c.Outputs.Images[0].Name, c.PinnedTag()), recipe, p.Recipes[c.Name()], c.Dir)
		if dependents := PinnedDependents(p.Components, c.Name()); len(dependents) > 0 {
			line += ", and in each pinned component that builds on it: " + strings.Join(dependents, ", ")
		}
		stale = append(stale, line+".")
	}
	if len(stale) > 0 {
		return nil, fmt.Errorf("a published tag never changes, and the recipe of %d pinned components changed with no new revision:\n%s",
			len(stale), strings.Join(stale, "\n"))
	}
	return published, nil
}

// PinnedDependents lists the pinned components that depend on the
// component, directly or through another one, by name.
func PinnedDependents(components map[string]*Component, name string) []string {
	var dependents []string
	for _, c := range sortedComponents(components) {
		if c.Name() != name && c.Pinned() && slices.Contains(Closure(components, c.Name()), name) {
			dependents = append(dependents, c.Name())
		}
	}
	return dependents
}

// pinnedDecision decides what a run does with a pinned component. Its
// version is its tag in every event. It builds and publishes when its
// tag is not published yet. Its jobs also run when its paths changed,
// such as a new smoke check, but a published tag is never pushed again.
func pinnedDecision(c *Component, published, pathsChanged bool, reason string) Decision {
	d := Decision{Publish: publishNone, Version: c.PinnedTag(), Newest: c.PinnedTag()}
	if published {
		d.Check, d.Reason = pathsChanged, c.PinnedTag()+" is published, and its recipe matches"
		if pathsChanged {
			d.Reason = reason + "; " + d.Reason
		}
		return d
	}
	d.Check, d.Changed, d.Why = true, true, c.PinnedTag()+" is not published yet"
	d.Reason = "builds: " + d.Why
	return d
}
