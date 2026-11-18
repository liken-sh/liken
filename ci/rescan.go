package main

import (
	"fmt"
	"slices"
	"strings"
)

// trivyConfig lists the files that set what a Trivy scan reports and
// where its results are uploaded: ci/trivy.go has the Trivy version and
// the scan arguments, and ci/templates/trivy.tmpl has the steps that
// install Trivy, upload each scan, and scan the published images again.
var trivyConfig = []string{"ci/trivy.go", "ci/templates/trivy.tmpl"}

// A ScanRun is one entry of the matrix of a trivy job, in the root
// workflow or in the scheduled scan.
type ScanRun struct {
	Image   string `json:"image"`
	Version string `json:"version"`
}

// Rescan returns the published images that the root workflow's trivy
// job scans again, and the reason.
//
// Each publish scans the images that it pushes, so the Security tab
// shows each image's results from the Trivy configuration of its newest
// publish. When a file of trivyConfig changes, the trivy job scans each
// image's newest published version with the new configuration and
// uploads under the category that the publish uses, so the new results
// replace the old ones. The job builds and pushes nothing, so a change
// to the scanner does not change the version of any component.
//
// Rescan returns images only on a push to main, and only when a file of
// trivyConfig changed since the newest main run that passed or when
// every job runs. A run on a branch or a pull request uploads under its
// own ref, which the Security tab's view of main does not read, and a
// release tag scans the images that it publishes.
//
// It leaves out each component that publishes in this run, because its
// publish job scans the pushed images with the new configuration. It
// also leaves out each pinned component whose tag is not published yet:
// a run that publishes pushes and scans that tag, and in any other run
// nothing is published at the tag.
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

// PublishedImages lists the images of each component that include
// accepts, at the version that a cluster pulls. The root workflow's
// trivy job and the scheduled scan both scan this list.
//
// A tracked component's images are at its newest published version, a
// release or a development build, because a cluster that follows the
// newest version pulls that one. A pinned component's images are at its
// pinned tag. A tracked component with nothing published is skipped,
// and the OS publishes no image.
func (p Planner) PublishedImages(include func(*Component) (bool, error)) ([]ScanRun, error) {
	var runs []ScanRun
	for _, c := range sortedComponents(p.Components) {
		if len(c.Outputs.Images) == 0 || c.Outputs.Channel {
			continue
		}
		ok, err := include(c)
		if err != nil {
			return nil, fmt.Errorf("choosing whether to scan the images of %s: %w", c.Name(), err)
		}
		if !ok {
			continue
		}
		version := c.PinnedTag()
		if !c.Pinned() {
			versions, err := p.Versions(c)
			if err != nil {
				return nil, fmt.Errorf("listing the published versions of %s: %w", c.Name(), err)
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
// workflow's trivy-db job then downloads Trivy's vulnerability database
// once for all the scans of the run. A dry run of a publish pushes and
// scans nothing.
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

// NewestPublished returns the newest release or development build
// among tags, or "" when there is neither. A development build is newer
// than the release that it is based on and older than the next release.
func NewestPublished(tags []string) string {
	release, dev := NewestRelease(tags), NewestDev(tags)
	m := devVersionParts.FindStringSubmatch(dev)
	if m == nil || m[1] < release {
		return release
	}
	return dev
}

// rescanNote returns the line of the plan summary that says which
// images the trivy job scans again, and why.
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
