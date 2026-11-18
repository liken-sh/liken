package main

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// scans plans the scheduled scan workflow, .github/workflows/scan.yaml.
// A vulnerability database can add an advisory after an image or a
// release was published, and the Security tab shows the finding only
// after a later scan. So the workflow scans every published image and
// the SBOM of the newest OS release on a schedule.
//
// The trivy job scans each image at the version that PublishedImages
// returns. A pinned component whose tag is not published yet has no
// image, so the plan checks the registry for the tag.
//
// The plan writes three outputs: trivy, the matrix of images, trivy-db,
// which is true when the matrix is not empty, and release, which is
// empty when the channel has no release.
func (e Env) scans(components map[string]*Component) error {
	planner := Planner{Components: components, Versions: e.Published.Versions}
	runs, err := planner.PublishedImages(func(c *Component) (bool, error) {
		if !c.Pinned() {
			return true, nil
		}
		_, published, err := e.Published.Recipe(c)
		return published, err
	})
	if err != nil {
		return err
	}
	release := ""
	for _, c := range sortedComponents(components) {
		if !c.Outputs.Channel {
			continue
		}
		versions, err := e.Published.Versions(c)
		if err != nil {
			return fmt.Errorf("listing the published versions of %s: %w", c.Name(), err)
		}
		release = NewestRelease(versions)
	}
	summary := scansSummary(runs, release)
	fmt.Fprint(e.Stdout, summary)
	if path := e.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
		if err := appendFile(path, summary); err != nil {
			return err
		}
	}
	if runs == nil {
		runs = []ScanRun{}
	}
	matrix, err := json.Marshal(runs)
	if err != nil {
		return err
	}
	if path := e.Getenv("GITHUB_OUTPUT"); path != "" {
		return appendFile(path, fmt.Sprintf("trivy=%s\ntrivy-db=%t\nrelease=%s\n", matrix, len(runs) > 0, release))
	}
	return nil
}

// scansSummary returns the job summary of the run, which lists the
// images and the release that the scheduled scan scans.
func scansSummary(runs []ScanRun, release string) string {
	var b strings.Builder
	b.WriteString("## The scan\n\n")
	if len(runs) == 0 {
		b.WriteString("Trivy scans no image: no image has a published version.\n\n")
	} else {
		refs := make([]string, len(runs))
		for i, run := range runs {
			refs[i] = "`" + run.Image + ":" + run.Version + "`"
		}
		fmt.Fprintf(&b, "Trivy scans %d published images: %s.\n\n", len(runs), strings.Join(refs, ", "))
	}
	if release == "" {
		b.WriteString("grype scans no OS release: the channel has none.\n")
	} else {
		fmt.Fprintf(&b, "grype scans the SBOM of the OS release `%s`.\n", release)
	}
	return b.String()
}

// scanFile is the scheduled scan workflow, written from
// templates/scan.yaml.tmpl.
const scanFile = ".github/workflows/scan.yaml"

// scanChannel is the data of the scheduled scan's job for the component
// that publishes to the channel. Name is the job's name, and the job
// uploads its findings under grype/<Name>-release. Cache is the Go
// cache of the component's module, because the job builds the toolkit
// from that module.
type scanChannel struct {
	Name, Dir string
	Cache     goCache
}

// scanData is the data of scan.yaml.tmpl. Channel is nil when no
// component publishes to a channel, and then the workflow scans only
// images.
func scanData(components map[string]*Component) map[string]any {
	var channel *scanChannel
	for _, c := range sortedComponents(components) {
		if c.Outputs.Channel {
			// The job restores the modules entry that the module's go job
			// saves. It has no build cache, because scan.yaml saves no
			// cache entry for it to restore.
			cache := goCache{Mod: path.Join(c.Dir, "go.mod"), Keys: []string{path.Join(c.Dir, "go.sum")}, Family: c.Dir}
			channel = &scanChannel{Name: c.Name(), Dir: c.Dir, Cache: cache}
		}
	}
	return map[string]any{"TrivyCache": trivyCache, "Channel": channel}
}

// grypeUpload is the data of the grype-upload template, which uploads
// grype/<Name>.sarif under the category grype/<Name>. When Dry is true,
// the step does not run in a dry run of a publish job.
type grypeUpload struct {
	Name string
	Dry  bool
}
