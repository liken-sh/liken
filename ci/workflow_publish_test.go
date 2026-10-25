package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestAPinnedImageJobLogsInOnEveryBranch(t *testing.T) {
	root := writeTree(t, map[string]string{
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	text := string(files[".github/workflows/component-base.yaml"])
	for _, want := range []string{"|| matrix.pinned }}\n        uses: docker/login-action@v3"} {
		if !strings.Contains(text, want) {
			t.Errorf("the workflow lacks %q:\n%s", want, text)
		}
	}
}

// A component's checks wait only for the pinned bases it builds on,
// whose layers its images read from the cache that the bases' jobs
// write. Its publish waits for the checks of every component in its
// closure, so it never publishes on a dependency that failed.
func TestTheChecksWaitForTheBasesAndThePublishWaitsForTheClosure(t *testing.T) {
	root := writeTree(t, map[string]string{
		"base/package.toml":  "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\n",
		"lib/package.toml":   "[package]\nname = \"lib\"\n",
		"app/package.toml":   "[package]\nname = \"app\"\n[depends]\ncomponents = [\"base\", \"lib\"]\n[outputs]\ndeploy = \"deploy\"\n",
		"top/package.toml":   "[package]\nname = \"top\"\n[depends]\ncomponents = [\"app\"]\n[outputs]\ndeploy = \"deploy\"\n",
		"liken/flux/VERSION": "v2.9.7\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	jobs := workflowJobs(t, files[".github/workflows/ci.yaml"])
	cases := map[string][]any{
		"app":          {"plan", "base"},
		"top":          {"plan"},
		"lib":          {"plan"},
		"base-publish": {"plan", "base"},
		"app-publish":  {"plan", "app", "base", "lib"},
		"top-publish":  {"plan", "top", "app", "base", "lib"},
	}
	for name, needs := range cases {
		if got := jobs[name]["needs"]; !reflect.DeepEqual(got, needs) {
			t.Errorf("%s needs %v, want %v", name, got, needs)
		}
	}
	if _, ok := jobs["lib-publish"]; ok {
		t.Error("a component with no outputs has a publish call")
	}
	publish := jobs["app-publish"]
	if publish["uses"] != "./.github/workflows/component-app.yaml" ||
		publish["if"] != "${{ !failure() && !cancelled() && fromJSON(needs.plan.outputs.components)['app'].publish != 'none' }}" ||
		publish["with"].(map[string]any)["stage"] != "publish" || jobs["app"]["with"].(map[string]any)["stage"] != "check" {
		t.Errorf("app's publish call is %v", publish)
	}
	component := workflowJobs(t, files[".github/workflows/component-app.yaml"])
	if needs, ok := component["publish"]["needs"]; ok || component["publish"]["if"] != "${{ inputs.stage == 'publish' || inputs.dryrun }}" {
		t.Errorf("the publish job needs %v, if %v", needs, component["publish"]["if"])
	}
}

// The check stage runs the publish job in its dry mode when the plan
// asks for a dry run. The dry run logs in to nothing, and the OS's dry
// run builds and boots a release under the lab's serial and uploads
// nothing.
func TestTheCheckStageRunsADryRunOfThePublish(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml":   "[package]\nname = \"app\"\n[outputs]\ndeploy = \"deploy\"\n[[outputs.images]]\nname = \"app\"\n",
		"os/package.toml":    "[package]\nname = \"os\"\n[[jobs]]\nname = \"build\"\ntoolchain = \"os\"\nrun = \"make all\"\n[outputs]\nchannel = true\n",
		"liken/flux/VERSION": "v2.9.7\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	calls := workflowJobs(t, files[".github/workflows/ci.yaml"])
	if with := calls["app"]["with"].(map[string]any); with["dryrun"] != "${{ fromJSON(needs.plan.outputs.components)['app'].dryrun }}" {
		t.Errorf("the check call passes dryrun %v", with["dryrun"])
	}
	if with := calls["app-publish"]["with"].(map[string]any); with["dryrun"] != false {
		t.Errorf("the publish call passes dryrun %v", with["dryrun"])
	}
	app := string(files[".github/workflows/component-app.yaml"])
	for _, want := range []string{
		"  publish:\n    if: ${{ inputs.stage == 'publish' || inputs.dryrun }}\n",
		"      - if: ${{ !inputs.dryrun }}\n        uses: docker/login-action@v3",
		`-mode "${{ inputs.dryrun && 'dry' || inputs.publish }}"`,
	} {
		if !strings.Contains(app, want) {
			t.Errorf("the app's workflow lacks %q", want)
		}
	}
	os := string(files[".github/workflows/component-os.yaml"])
	for _, want := range []string{
		`if [ "${{ inputs.dryrun }}" = true ]; then version="$(date -u +%Y.%m.%d)-000"; fi`,
		"      - name: publish to the channel\n        if: ${{ !inputs.dryrun }}\n",
		"      - name: check the publish script\n        if: ${{ inputs.dryrun }}\n",
	} {
		if !strings.Contains(os, want) {
			t.Errorf("the OS's workflow lacks %q", want)
		}
	}
	// A tag that changes no Go code runs no check job, so the publish
	// job writes its own coverage profile instead of downloading one.
	if strings.Contains(os, "download-artifact") {
		t.Error("the OS's publish job downloads an artifact that a tag may never upload")
	}
}

func TestThePublishJobSetsUpOnlyWhatTheOutputsNeed(t *testing.T) {
	root := writeTree(t, map[string]string{
		"operator/package.toml": "[package]\nname = \"operator\"\n[outputs]\ndeploy = \"deploy\"\n[[outputs.images]]\nname = \"operator\"\n",
		"crd/package.toml":      "[package]\nname = \"crd\"\n[outputs]\ndeploy = \"deploy\"\n",
		"base/package.toml":     "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\n",
		"liken/flux/VERSION":    "v2.9.7\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string][]string{
		"operator": {"docker/setup-buildx-action@v3", "docker/login-action@v3", "fluxcd/flux2/action@v2.9.7"},
		"crd":      {"docker/login-action@v3", "fluxcd/flux2/action@v2.9.7"},
		"base":     {"docker/setup-buildx-action@v3", "docker/login-action@v3"},
	}
	for name, want := range cases {
		publish := workflowJobs(t, files[".github/workflows/component-"+name+".yaml"])["publish"]
		var got []string
		for _, step := range publish["steps"].([]any) {
			uses, _ := step.(map[string]any)["uses"].(string)
			if strings.HasPrefix(uses, "docker/") || strings.HasPrefix(uses, "fluxcd/") {
				got = append(got, uses)
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s's publish job sets up %v, want %v", name, got, want)
		}
	}
}
