package main

import (
	"fmt"
	"slices"
	"strings"
)

// trivyConfig lists the files that decide what a Trivy scan reports
// and where its results go: the Trivy version and the arguments of a
// scan in ci/trivy.go, and the steps that install Trivy, upload each
// scan, and scan the published images again in ci/templates/trivy.tmpl.
var trivyConfig = []string{"ci/trivy.go", "ci/templates/trivy.tmpl"}

// A ScanRun is one entry of the matrix of a trivy job, in the root
// workflow or in the scheduled scan: an image and the published
// version that it scans.
type ScanRun struct {
	Image   string `json:"image"`
	Version string `json:"version"`
}

// Rescan decides which published images the root workflow's trivy job
// scans again, and why.
//
// A publish scans each image that it pushes, so the Security tab holds
// the results of the Trivy configuration that each image's newest
// publish ran. When that configuration changes, the trivy job scans
// each image's newest published version with the new one, and uploads
// each result under the category that the publish uses, so the new
// results replace the old ones. Nothing builds or pushes, so no
// component gets a new version for a change to the scanner.
//
// The rescan runs on a push to main, when a file of trivyConfig changed
// since the newest main run that passed, or when every job runs. A
// push to a branch or a pull request uploads no result for main, and a
// release tag scans each image that it publishes. A change to any other
// file scans nothing again: the images did not change, and the
// configuration that scanned them did not either.
//
// A component that publishes in this run is left out, because its
// publish job scans the images it pushes, with the new configuration.
// A pinned component whose tag publishes in this run is left out for
// the same reason. PublishedImages gives the version of each image.
func (p Planner) Rescan(e Event, decisions map[string]Decision) ([]ScanRun, string, error) {
	if !e.Main() {
		return nil, "only a push to main scans the published images again", nil
	}
	list, all := p.Comparisons(e)
	why := "every job runs"
	if all == "" {
		diff, err := p.readDiffs(list, e.Head)
		if err != nil {
			return nil, "", err
		}
		i := slices.IndexFunc(diff.Files, func(file string) bool { return slices.Contains(trivyConfig, file) })
		if i < 0 {
			return nil, "no file of the Trivy configuration changed: " + strings.Join(trivyConfig, ", "), nil
		}
		why = "changed: " + diff.Files[i]
	}
	runs, err := p.PublishedImages(func(c *Component) (bool, error) {
		d := decisions[c.Name()]
		return d.Publish == publishNone && !(c.Pinned() && d.Changed), nil
	})
	if err != nil {
		return nil, "", err
	}
	if len(runs) == 0 {
		why += ", and each image publishes in this run or has no published version"
	}
	return runs, why, nil
}

// PublishedImages lists each published image of the components that
// include takes, at the version that a cluster pulls. Both the root
// workflow's trivy job and the scheduled scan scan this list.
//
// A tracked component's images are at its newest published version, a
// release or a development build, because a cluster that follows the
// newest version pulls it. A pinned component's images are at its
// published tag. A component with nothing published has nothing to
// scan, and the OS publishes no image.
func (p Planner) PublishedImages(include func(*Component) (bool, error)) ([]ScanRun, error) {
	var runs []ScanRun
	for _, c := range sortedComponents(p.Components) {
		if len(c.Outputs.Images) == 0 || c.Outputs.Channel {
			continue
		}
		ok, err := include(c)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Name(), err)
		}
		if !ok {
			continue
		}
		version := c.PinnedTag()
		if !c.Pinned() {
			versions, err := p.Versions(c)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", c.Name(), err)
			}
			if version = NewestPublished(versions); version == "" {
				continue
			}
		}
		for _, image := range c.Outputs.Images {
			runs = append(runs, ScanRun{Image: image.Name, Version: version})
		}
	}
	return runs, nil
}

// ScansImages is true when a job of the run scans an image: the trivy
// job, or the publish of a component that pushes images. The root
// workflow's trivy-db job then downloads Trivy's vulnerability
// database once, for every scan of the run. A dry run of a publish
// pushes nothing and scans nothing, and the OS publishes no image.
func ScansImages(components map[string]*Component, decisions map[string]Decision, rescan []ScanRun) bool {
	if len(rescan) > 0 {
		return true
	}
	for _, c := range sortedComponents(components) {
		if len(c.Outputs.Images) > 0 && !c.Outputs.Channel && decisions[c.Name()].Publish != publishNone {
			return true
		}
	}
	return false
}

// NewestPublished is the newest version among tags, a release or a
// development build, or "" when there is neither. A development build
// comes after the release that it names and before the next release.
func NewestPublished(tags []string) string {
	release, dev := NewestRelease(tags), NewestDev(tags)
	m := devVersionParts.FindStringSubmatch(dev)
	if m == nil || m[1] < release {
		return release
	}
	return dev
}

// rescanNote says in the plan's summary which images the trivy job
// scans again, and why.
func rescanNote(runs []ScanRun, why string) string {
	if len(runs) == 0 {
		return "Trivy scans no published image again: " + why + "."
	}
	refs := make([]string, len(runs))
	for i, run := range runs {
		refs[i] = "`" + run.Image + ":" + run.Version + "`"
	}
	return fmt.Sprintf("Trivy scans %d published images again, with no build and no push (%s): %s.", len(runs), why, strings.Join(refs, ", "))
}
