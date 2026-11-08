package main

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAPinnedImageJobLogsInOnEveryBranch(t *testing.T) {
	root := writeTree(t, map[string]string{
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n",
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
	for _, want := range []string{"|| matrix.pinned }}\n        uses: " + pinnedText("docker/login-action")} {
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
		"base/package.toml":  "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n",
		"lib/package.toml":   "[package]\nname = \"lib\"\n",
		"app/package.toml":   "[package]\nname = \"app\"\n[depends]\ncomponents = [\"base\", \"lib\"]\n[outputs]\ndeploy = \"deploy\"\n",
		"top/package.toml":   "[package]\nname = \"top\"\n[depends]\ncomponents = [\"app\"]\n[outputs]\ndeploy = \"deploy\"\n",
		"liken/flux/VERSION": testFlux + "\n",
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
		"base-publish": {"plan", "base", "trivy-db"},
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
		"app/package.toml":   "[package]\nname = \"app\"\n[outputs]\ndeploy = \"deploy\"\n[[outputs.images]]\nname = \"app\"\ndescription = \"The app image.\"\n",
		"os/package.toml":    "[package]\nname = \"os\"\n[[jobs]]\nname = \"build\"\ntoolchain = \"os\"\nrun = \"make all\"\n[outputs]\nchannel = true\n",
		"liken/flux/VERSION": testFlux + "\n",
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
		"      - if: ${{ !inputs.dryrun }}\n        uses: " + pinnedText("docker/login-action"),
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
		"operator/package.toml": "[package]\nname = \"operator\"\n[outputs]\ndeploy = \"deploy\"\n[[outputs.images]]\nname = \"operator\"\ndescription = \"The operator image.\"\n",
		"crd/package.toml":      "[package]\nname = \"crd\"\n[outputs]\ndeploy = \"deploy\"\n",
		"base/package.toml":     "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n",
		"liken/flux/VERSION":    testFlux + "\n",
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
		"operator": {pinnedRef("docker/setup-buildx-action"), pinnedRef("docker/login-action"), pinnedRef("fluxcd/flux2/action")},
		"crd":      {pinnedRef("docker/login-action"), pinnedRef("fluxcd/flux2/action")},
		"base":     {pinnedRef("docker/setup-buildx-action"), pinnedRef("docker/login-action")},
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

// trivyFixture is a pinned base with two images, and a component that
// publishes only a deploy artifact, so it pushes no image to scan.
func trivyFixture(t *testing.T) map[string][]byte {
	t.Helper()
	root := writeTree(t, map[string]string{
		"base/package.toml":  "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n[[outputs.images]]\nname = \"base-dev\"\ndescription = \"The base-dev image.\"\n",
		"app/package.toml":   "[package]\nname = \"app\"\n[outputs]\ndeploy = \"deploy\"\n",
		"liken/flux/VERSION": testFlux + "\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// The publish of a component with images installs Trivy, scans each
// image it pushes, and uploads each scan under the image's own
// category. Both calls of its workflow grant the upload, because
// GitHub checks every job of a called workflow against the call.
func TestThePublishUploadsATrivyScanOfEachImage(t *testing.T) {
	files := trivyFixture(t)
	publish := workflowJobs(t, files[".github/workflows/component-base.yaml"])["publish"]
	var uploads []string
	trivy, sarif := "", false
	for _, step := range publish["steps"].([]any) {
		step := step.(map[string]any)
		with, _ := step["with"].(map[string]any)
		env, _ := step["env"].(map[string]any)
		switch uses, _ := step["uses"].(string); {
		case env["TRIVY_VERSION"] != nil:
			trivy, _ = env["TRIVY_VERSION"].(string)
		case strings.HasPrefix(uses, "github/codeql-action/upload-sarif@"):
			uploads = append(uploads, with["sarif_file"].(string)+" "+with["category"].(string))
		}
		if run, _ := step["run"].(string); strings.Contains(run, "go run . publish ") && strings.Contains(run, ` -sarif "${{ github.workspace }}/trivy"`) {
			sarif = true
		}
	}
	if trivy != trivyVersion || !sarif {
		t.Errorf("trivy %q, the publish writes the scans: %v", trivy, sarif)
	}
	if want := []string{"trivy/base.sarif trivy/base", "trivy/base-dev.sarif trivy/base-dev"}; !reflect.DeepEqual(uploads, want) {
		t.Errorf("uploads %v, want %v", uploads, want)
	}
	if got := publish["permissions"].(map[string]any)["security-events"]; got != "write" {
		t.Errorf("the publish job's security-events: %v", got)
	}
}

// The root workflow's trivy job runs one scan for each image that the
// plan names, and uploads it under the image's own category, the one
// that the image's publish uploads under. It runs only when the plan
// names an image, and it never fails the run.
func TestTheTrivyJobScansEachImageThatThePlanNames(t *testing.T) {
	root := trivyFixture(t)[".github/workflows/ci.yaml"]
	var doc struct {
		Jobs map[string]struct {
			If              string `yaml:"if"`
			ContinueOnError bool   `yaml:"continue-on-error"`
			Permissions     map[string]string
			Strategy        struct {
				Matrix struct {
					Include string `yaml:"include"`
				} `yaml:"matrix"`
			} `yaml:"strategy"`
			Outputs map[string]string `yaml:"outputs"`
			Steps   []struct {
				Uses string            `yaml:"uses"`
				Run  string            `yaml:"run"`
				With map[string]string `yaml:"with"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(root, &doc); err != nil {
		t.Fatal(err)
	}
	if got := doc.Jobs["plan"].Outputs["trivy"]; got != "${{ steps.plan.outputs.trivy }}" {
		t.Errorf("the plan's trivy output is %q", got)
	}
	job := doc.Jobs["trivy"]
	if job.If != "${{ !cancelled() && fromJSON(needs.plan.outputs.trivy)[0] != null }}" || !job.ContinueOnError ||
		job.Strategy.Matrix.Include != "${{ fromJSON(needs.plan.outputs.trivy) }}" || job.Permissions["security-events"] != "write" {
		t.Errorf("the trivy job: %+v", job)
	}
	var trivy, scan, upload string
	for _, step := range job.Steps {
		switch {
		case step.Env["TRIVY_VERSION"] != "":
			trivy = step.Env["TRIVY_VERSION"]
		case strings.Contains(step.Run, "go run . scan "):
			scan = step.Run
		case strings.HasPrefix(step.Uses, "github/codeql-action/upload-sarif@"):
			upload = step.With["sarif_file"] + " " + step.With["category"]
		}
	}
	if trivy != trivyVersion || scan != `go run . scan -root .. -image "$IMAGE" -version "$VERSION" -sarif "${{ github.workspace }}/trivy"` ||
		upload != "trivy/${{ matrix.image }}.sarif trivy/${{ matrix.image }}" {
		t.Errorf("trivy %q, scan %q, upload %q", trivy, scan, upload)
	}
}

// The trivy-db job downloads Trivy's database once for the run, and
// every job that scans waits for it and restores the database before
// it installs Trivy. A publish that pushes no image does not wait.
func TestEveryScanRestoresTheDatabaseThatTheTrivyDBJobSaves(t *testing.T) {
	files := trivyFixture(t)
	jobs := workflowJobs(t, files[".github/workflows/ci.yaml"])
	db := jobs["trivy-db"]
	if db["if"] != "${{ needs.plan.outputs.trivy-db == 'true' }}" || db["permissions"].(map[string]any)["actions"] != "write" {
		t.Errorf("the trivy-db job: %v", db)
	}
	needs := map[string]bool{}
	for _, job := range []string{"trivy", "base-publish", "app-publish"} {
		for _, need := range jobs[job]["needs"].([]any) {
			needs[job] = needs[job] || need == "trivy-db"
		}
	}
	if want := map[string]bool{"trivy": true, "base-publish": true, "app-publish": false}; !reflect.DeepEqual(needs, want) {
		t.Errorf("the jobs that wait for trivy-db: %v, want %v", needs, want)
	}
	for name, steps := range map[string]any{
		"the trivy job":          jobs["trivy"]["steps"],
		"the base's publish job": workflowJobs(t, files[".github/workflows/component-base.yaml"])["publish"]["steps"],
	} {
		var order []string
		for _, step := range steps.([]any) {
			step := step.(map[string]any)
			with, _ := step["with"].(map[string]any)
			env, _ := step["env"].(map[string]any)
			switch uses, _ := step["uses"].(string); {
			case uses == pinnedRef("actions/cache/restore") && with["path"] == "~/.cache/trivy/db":
				order = append(order, "restore "+with["restore-keys"].(string))
			case env["TRIVY_VERSION"] != nil:
				order = append(order, "install")
			}
		}
		if want := []string{"restore trivy-db-", "install"}; !reflect.DeepEqual(order, want) {
			t.Errorf("%s: %v, want %v", name, order, want)
		}
	}
}

func TestOnlyAComponentThatUploadsIsGrantedSecurityEvents(t *testing.T) {
	jobs := workflowJobs(t, trivyFixture(t)[".github/workflows/ci.yaml"])
	cases := map[string]any{"base": "write", "base-publish": "write", "app": nil, "app-publish": nil}
	for name, want := range cases {
		if got := jobs[name]["permissions"].(map[string]any)["security-events"]; got != want {
			t.Errorf("%s grants security-events %v, want %v", name, got, want)
		}
	}
}

// The plan job runs `ci bases` once, and each build of the run writes
// its output as the override file and reads it after the bake file, so
// every build of the run uses the same published bases.
func TestEveryBuildReadsTheBasesThatThePlanJobChecked(t *testing.T) {
	root := writeTree(t, map[string]string{
		"base/package.toml": "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\ncache-mount = true\n",
		"base/Dockerfile":   "FROM scratch AS cache-seed\nFROM scratch AS cache-export\nFROM scratch\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	ci := string(files[".github/workflows/ci.yaml"])
	component := string(files[".github/workflows/component-base.yaml"])
	write := "      - name: write the published bases\n        env:\n          BASES: ${{ inputs.bases }}\n        run: printf '%s\\n' \"$BASES\" > docker-bake.override.hcl\n"
	for _, c := range []struct{ file, text, want string }{
		{"ci.yaml", ci, "      bases: ${{ steps.bases.outputs.bases }}\n"},
		{"ci.yaml", ci, "      - id: bases\n        working-directory: ci\n        run: go run . bases -root ..\n"},
		{"ci.yaml", ci, "      images: ${{ toJSON(fromJSON(needs.plan.outputs.components)['base'].images) }}\n      bases: ${{ needs.plan.outputs.bases }}\n"},
		{"ci.yaml", ci, "      images: '[]'\n      bases: ${{ needs.plan.outputs.bases }}\n"},
		{"component-base.yaml", component, "      - uses: " + pinnedText("actions/checkout") + "\n\n      # The plan job checked the published image of each pinned base,\n"},
		{"component-base.yaml", component, write + "\n      - uses: " + pinnedText("docker/setup-buildx-action")},
		{"component-base.yaml", component, write + "\n      # Each image builds again"},
		{"component-base.yaml", component, "files: |\n            docker-bake.hcl\n            docker-bake.override.hcl\n"},
		{"component-base.yaml", component, `docker buildx bake -f docker-bake.hcl -f docker-bake.override.hcl "${{ matrix.image }}-cache-seed"`},
		{"component-base.yaml", component, `docker buildx bake -f docker-bake.hcl -f docker-bake.override.hcl "${{ matrix.image }}-cache-export"`},
		{"component-base.yaml", component, "      - if: ${{ !matrix.published }}\n        uses: " + pinnedText("docker/bake-action")},
		{"component-base.yaml", component, "        if: ${{ matrix.published }}\n        run: docker pull \"ghcr.io/liken-sh/${{ matrix.image }}:${{ inputs.version }}\"\n"},
	} {
		if !strings.Contains(c.text, c.want) {
			t.Errorf("%s lacks:\n%s", c.file, c.want)
		}
	}
}
