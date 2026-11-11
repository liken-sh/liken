package main

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"
)

// scans plans the scheduled scan workflow, .github/workflows/scan.yaml.
// A vulnerability that a database records after an image or a release
// was published reaches the Security tab only when something scans
// that image or release again, so the workflow scans every published
// image and the newest OS release on a timer.
//
// The trivy job scans each image at the version that a cluster pulls,
// as PublishedImages gives it. A pinned component whose tag is not
// published yet has no image to scan, so the plan reads whether the tag
// exists. The OS's job scans the SBOM of the newest release on the
// channel, because the OS publishes releases and no development build. The
// plan writes the matrix as the output trivy, whether the trivy-db job
// runs as trivy-db, and the release as release, which is empty when
// the channel holds none.
func (w World) scans(components map[string]*Component) error {
	planner := Planner{Components: components, Versions: w.Published.Versions}
	runs, err := planner.PublishedImages(func(c *Component) (bool, error) {
		if !c.Pinned() {
			return true, nil
		}
		_, published, err := w.Published.Recipe(c)
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
		versions, err := w.Published.Versions(c)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name(), err)
		}
		release = NewestRelease(versions)
	}
	summary := scansSummary(runs, release)
	fmt.Fprint(w.Stdout, summary)
	if path := w.Getenv("GITHUB_STEP_SUMMARY"); path != "" {
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
	if path := w.Getenv("GITHUB_OUTPUT"); path != "" {
		return appendFile(path, fmt.Sprintf("trivy=%s\ntrivy-db=%t\nrelease=%s\n", matrix, len(runs) > 0, release))
	}
	return nil
}

// scansSummary says on the run page which images and which release the
// scheduled scan scans.
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
		b.WriteString("grype scans no OS release: the channel holds none.\n")
	} else {
		fmt.Fprintf(&b, "grype scans the SBOM of the OS release `%s`.\n", release)
	}
	return b.String()
}

// scanFile is the scheduled scan workflow, which the generator writes
// from ci/templates/scan.yaml.tmpl.
const scanFile = ".github/workflows/scan.yaml"

// scanChannel is the data of the scheduled scan's job for the
// component that publishes to the channel: its name, which names the
// job and the category grype/<name>-release of a published release's
// scan, its directory, and the Go cache of its module, which the
// toolkit builds from.
type scanChannel struct {
	Name, Dir string
	Cache     goCache
}

// scanData is the data of scan.yaml.tmpl. Channel is nil when no
// component publishes to a channel, and the workflow then scans images
// only.
func scanData(components map[string]*Component) map[string]any {
	var channel *scanChannel
	for _, c := range sortedComponents(components) {
		if c.Outputs.Channel {
			// The job restores the modules entry of the module's go job,
			// and keeps no build cache: a scheduled run saves no entry.
			cache := goCache{Mod: path.Join(c.Dir, "go.mod"), Keys: []string{path.Join(c.Dir, "go.sum")}, Family: c.Dir}
			channel = &scanChannel{Name: c.Name(), Dir: c.Dir, Cache: cache}
		}
	}
	return map[string]any{"TrivyCache": trivyCache, "Channel": channel}
}

// grypeUpload is the data of the grype-upload template: the name of
// the component whose release the step uploads the scan of, and Dry,
// which is true in a publish job, which uploads nothing in a dry run.
type grypeUpload struct {
	Name string
	Dry  bool
}
