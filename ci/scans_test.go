package main

import (
	"os"
	"path/filepath"
	"testing"
)

// scansTree is a repository with a pinned base, a tracked operator
// with two images, and the OS, which publishes to the channel.
func scansTree(t *testing.T) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"base/package.toml":     "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n",
		"operator/package.toml": "[package]\nname = \"operator\"\n[depends]\ncomponents = [\"base\"]\n[[outputs.images]]\nname = \"operator\"\ndescription = \"The operator image.\"\n[[outputs.images]]\nname = \"operator-cli\"\ndescription = \"The operator-cli image.\"\n",
		"liken/package.toml":    "[package]\nname = \"liken\"\n[outputs]\nchannel = true\n",
	})
}

const twoReleases = `releases:
  - version: 2026.09.27-001
    digest: sha256:abc
  - version: 2026.09.28-001
    digest: sha256:def
`

// The scans command plans the scheduled scan: every published image at
// the version that a cluster pulls, and the newest release of the OS.
// A pinned tag that is not published, a component with no published
// version, and a channel with no release give nothing to scan.
func TestTheScheduledScanNamesEveryPublishedImageAndTheNewestRelease(t *testing.T) {
	cases := []struct {
		name     string
		base     bool
		operator []string
		versions string
		output   string
	}{
		{"everything is published", true, operatorPublished, twoReleases,
			`trivy=[{"image":"base","version":"20260928-1"},{"image":"operator","version":"2026.09.27-001-dev-003-abcdef01"},{"image":"operator-cli","version":"2026.09.27-001-dev-003-abcdef01"}]` +
				"\ntrivy-db=true\nrelease=2026.09.28-001\n"},
		{"the base's tag is not published", false, operatorPublished, twoReleases,
			`trivy=[{"image":"operator","version":"2026.09.27-001-dev-003-abcdef01"},{"image":"operator-cli","version":"2026.09.27-001-dev-003-abcdef01"}]` +
				"\ntrivy-db=true\nrelease=2026.09.28-001\n"},
		{"nothing is published", false, nil, "releases: []\n", "trivy=[]\ntrivy-db=false\nrelease=\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeRegistry{tags: map[string][]string{}}
			if c.base {
				fake.push("base", "20260928-1", "sha256:base", map[string]string{recipeLabel: "any"})
			}
			if c.operator != nil {
				fake.tags["operator"] = c.operator
			}
			w := newWorld(t, fake)
			w.Published.Channel = channel(t, c.versions)
			output := filepath.Join(t.TempDir(), "output")
			w.env["GITHUB_OUTPUT"] = output
			if err := w.run([]string{"scans", "-root", scansTree(t)}); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(output); string(got) != c.output {
				t.Errorf("the output is\n%s\nwant\n%s", got, c.output)
			}
		})
	}
}

func TestTheScheduledScanSaysWhatItScans(t *testing.T) {
	cases := []struct {
		name    string
		runs    []ScanRun
		release string
		want    string
	}{
		{"images and a release", everyImage[:2], "2026.09.28-001",
			"## The scan\n\nTrivy scans 2 published images: `base:20260928-1`, `operator:2026.09.27-001-dev-003-abcdef01`.\n\n" +
				"grype scans the SBOM of the OS release `2026.09.28-001`.\n"},
		{"nothing", nil, "",
			"## The scan\n\nTrivy scans no image: no image has a published version.\n\n" +
				"grype scans no OS release: the channel holds none.\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := scansSummary(c.runs, c.release); got != c.want {
				t.Errorf("the summary is\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}
