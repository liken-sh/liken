package main

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// scanFixture is the workflows of a repository with a pinned base, a
// component that publishes only a deploy artifact, and the OS, which
// publishes to the channel.
func scanFixture(t *testing.T) map[string][]byte {
	t.Helper()
	root := writeTree(t, map[string]string{
		"base/package.toml":      "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n",
		"app/package.toml":       "[package]\nname = \"app\"\n[outputs]\ndeploy = \"deploy\"\n",
		"os/package.toml":        "[package]\nname = \"os\"\n[outputs]\nchannel = true\n",
		"liken/flux/VERSION":     testFlux + "\n",
		"toolchains/images.toml": fixtureBuildKitImages,
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

// scanStep is the part of a step that the scan tests read.
type scanStep struct {
	Name             string            `yaml:"name"`
	If               string            `yaml:"if"`
	Uses             string            `yaml:"uses"`
	Run              string            `yaml:"run"`
	WorkingDirectory string            `yaml:"working-directory"`
	ContinueOnError  bool              `yaml:"continue-on-error"`
	TimeoutMinutes   int               `yaml:"timeout-minutes"`
	With             map[string]string `yaml:"with"`
	Env              map[string]string `yaml:"env"`
}

// scanJob is the part of a job that the scan tests read.
type scanJob struct {
	Needs           any               `yaml:"needs"`
	If              string            `yaml:"if"`
	TimeoutMinutes  int               `yaml:"timeout-minutes"`
	ContinueOnError bool              `yaml:"continue-on-error"`
	Permissions     map[string]string `yaml:"permissions"`
	Outputs         map[string]string `yaml:"outputs"`
	Steps           []scanStep        `yaml:"steps"`
}

// scanWorkflow parses a generated workflow into its triggers and jobs.
func scanWorkflow(t *testing.T, data []byte) (map[string]any, map[string]scanJob) {
	t.Helper()
	var doc struct {
		On   map[string]any     `yaml:"on"`
		Jobs map[string]scanJob `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%v:\n%s", err, data)
	}
	return doc.On, doc.Jobs
}

// The scheduled scan runs on a timer and by hand, and its plan job
// writes the outputs that the trivy-db and trivy jobs read.
func TestTheScanWorkflowRunsOnATimerAndByHand(t *testing.T) {
	on, jobs := scanWorkflow(t, scanFixture(t)[".github/workflows/scan.yaml"])
	schedule := on["schedule"].([]any)[0].(map[string]any)
	if _, dispatch := on["workflow_dispatch"]; schedule["cron"] != "43 4 */3 * *" || !dispatch || len(on) != 2 {
		t.Errorf("the triggers: %v", on)
	}
	plan := jobs["plan"]
	want := map[string]string{
		"trivy":    "${{ steps.plan.outputs.trivy }}",
		"trivy-db": "${{ steps.plan.outputs.trivy-db }}",
		"release":  "${{ steps.plan.outputs.release }}",
	}
	if !reflect.DeepEqual(plan.Outputs, want) {
		t.Errorf("the plan's outputs: %v", plan.Outputs)
	}
	if !slices.ContainsFunc(plan.Steps, func(s scanStep) bool { return s.Run == "go run . scans -root .." && s.WorkingDirectory == "ci" }) {
		t.Errorf("the plan does not run the scans command: %+v", plan.Steps)
	}
}

// The scheduled scan runs the same trivy-db and trivy jobs that the
// root workflow runs, so a scan reports the same way in both, and
// uploads under the same category.
func TestTheScanWorkflowRunsTheRootWorkflowsTrivyJobs(t *testing.T) {
	files := scanFixture(t)
	root := workflowJobs(t, files[".github/workflows/ci.yaml"])
	scan := workflowJobs(t, files[".github/workflows/scan.yaml"])
	for _, name := range []string{"trivy-db", "trivy"} {
		if !reflect.DeepEqual(scan[name], root[name]) {
			t.Errorf("the %s job differs:\nscan.yaml %v\nci.yaml %v", name, scan[name], root[name])
		}
	}
}

// Every job of the scheduled scan has a limit and states its
// permissions, and only a job that uploads may write to code scanning.
func TestEveryJobOfTheScanWorkflowHasATimeoutAndItsOwnPermissions(t *testing.T) {
	_, jobs := scanWorkflow(t, scanFixture(t)[".github/workflows/scan.yaml"])
	want := map[string]string{"plan": "", "trivy-db": "", "trivy": "write", "os": "write"}
	if len(jobs) != len(want) {
		t.Errorf("the jobs: %v", jobs)
	}
	for name, events := range want {
		job := jobs[name]
		if job.TimeoutMinutes == 0 || job.Permissions["contents"] != "read" || job.Permissions["security-events"] != events {
			t.Errorf("%s: timeout %d, permissions %v", name, job.TimeoutMinutes, job.Permissions)
		}
	}
}

// The OS's job of the scheduled scan fetches the newest release's SBOM
// with the toolkit, which checks the release's signature, scans it with
// grype, and uploads the findings under grype/<component>-release. It
// runs only
// when the channel holds a release, and it never fails the run.
func TestTheScanWorkflowScansTheSBOMOfTheNewestRelease(t *testing.T) {
	_, jobs := scanWorkflow(t, scanFixture(t)[".github/workflows/scan.yaml"])
	job := jobs["os"]
	if job.If != "${{ needs.plan.outputs.release != '' }}" || !job.ContinueOnError || job.Needs != "plan" {
		t.Errorf("the os job: %+v", job)
	}
	var order []string
	for _, step := range job.Steps {
		switch {
		case strings.Contains(step.Run, `go run ./cli sbom -json "$VERSION" > "${{ github.workspace }}/grype/sbom.cdx.json"`):
			order = append(order, "sbom "+step.Env["VERSION"]+" in "+step.WorkingDirectory)
		case strings.Contains(step.Run, "releases/sbom/vulnerabilities.sh"):
			order = append(order, step.Run+" in "+step.WorkingDirectory)
		case strings.HasPrefix(step.Uses, "github/codeql-action/upload-sarif@"):
			order = append(order, "upload "+step.With["sarif_file"]+" "+step.With["category"]+" if "+step.If)
		}
	}
	want := []string{
		"sbom ${{ needs.plan.outputs.release }} in os",
		`releases/sbom/vulnerabilities.sh "${{ github.workspace }}/grype/sbom.cdx.json" "${{ github.workspace }}/grype/os-release.sarif" in os`,
		"upload grype/os-release.sarif grype/os-release if ${{ !cancelled() && hashFiles('grype/os-release.sarif') != '' }}",
	}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("the steps:\n%s\nwant\n%s", strings.Join(order, "\n"), strings.Join(want, "\n"))
	}
}

// The OS's publish job scans the release that it just published and
// uploads under grype/<component>-release, the category of the
// scheduled scan, so the new release's findings replace the old
// release's. A dry run scans
// nothing, and a scan never fails the publish.
func TestTheChannelPublishScansTheReleaseAfterItUploads(t *testing.T) {
	_, jobs := scanWorkflow(t, scanFixture(t)[".github/workflows/component-os.yaml"])
	publish := jobs["publish"]
	var order []string
	for _, step := range publish.Steps {
		switch {
		case strings.Contains(step.Run, "releases/publish.sh") && !strings.Contains(step.Run, "bash -n"):
			order = append(order, "publish")
		case strings.Contains(step.Run, "releases/sbom/vulnerabilities.sh"):
			if step.If != "${{ !inputs.dryrun }}" || !step.ContinueOnError || step.TimeoutMinutes == 0 {
				t.Errorf("the scan step: %+v", step)
			}
			order = append(order, step.Run+" in "+step.WorkingDirectory)
		case strings.HasPrefix(step.Uses, "github/codeql-action/upload-sarif@"):
			if !step.ContinueOnError {
				t.Errorf("the upload step fails the publish: %+v", step)
			}
			order = append(order, "upload "+step.With["sarif_file"]+" "+step.With["category"]+" if "+step.If)
		}
	}
	want := []string{
		"publish",
		`releases/sbom/vulnerabilities.sh "releases/dist/${{ inputs.version }}/sbom.cdx.json" "${{ github.workspace }}/grype/os-release.sarif" in os`,
		"upload grype/os-release.sarif grype/os-release if ${{ !cancelled() && !inputs.dryrun && hashFiles('grype/os-release.sarif') != '' }}",
	}
	if !reflect.DeepEqual(order, want) {
		t.Errorf("the steps:\n%s\nwant\n%s", strings.Join(order, "\n"), strings.Join(want, "\n"))
	}
	if publish.Permissions["security-events"] != "write" {
		t.Errorf("the publish job's permissions: %v", publish.Permissions)
	}
}

// GitHub checks every job of a called workflow against the call, so
// both calls of the OS's workflow grant what its publish job uploads
// with.
func TestBothCallsOfTheOSWorkflowGrantSecurityEvents(t *testing.T) {
	jobs := workflowJobs(t, scanFixture(t)[".github/workflows/ci.yaml"])
	for _, name := range []string{"os", "os-publish"} {
		if got := jobs[name]["permissions"].(map[string]any)["security-events"]; got != "write" {
			t.Errorf("%s grants security-events %v", name, got)
		}
	}
}

// A repository with no component that publishes to a channel has no
// release to scan, and its scheduled scan has no job for one.
// grypeTree is the workflows of an OS whose build job scans what it
// built and a check job that scans nothing.
func grypeTree(t *testing.T) map[string][]byte {
	t.Helper()
	root := writeTree(t, map[string]string{
		"os/package.toml": "[package]\nname = \"os\"\n[[jobs]]\nname = \"build\"\ntoolchain = \"os\"\ngrype = true\n" +
			"run = \"\"\"\nmake all\nmake vulnerabilities SARIF=\"$GITHUB_WORKSPACE/grype/os.sarif\"\n\"\"\"\n" +
			"[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"make test\"\n[outputs]\nchannel = true\n",
		"toolchains/images.toml": fixtureBuildKitImages,
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

// A job with grype = true uploads the scan that its command writes,
// under grype/<component>, on every ref that it runs on, and only that
// job may write to code scanning. The build job keeps packages: write,
// because on main it pushes the userland that it compiled.
func TestAJobThatScansItsBuildUploadsTheScan(t *testing.T) {
	_, jobs := scanWorkflow(t, grypeTree(t)[".github/workflows/component-os.yaml"])
	cases := []struct {
		job         string
		permissions map[string]string
		uploads     []string
	}{
		{"build", map[string]string{"contents": "read", "packages": "write", "security-events": "write"},
			[]string{"grype/os.sarif grype/os if ${{ !cancelled() && hashFiles('grype/os.sarif') != '' }}"}},
		{"go", nil, nil},
	}
	for _, c := range cases {
		t.Run(c.job, func(t *testing.T) {
			var uploads []string
			for _, step := range jobs[c.job].Steps {
				if strings.HasPrefix(step.Uses, "github/codeql-action/upload-sarif@") {
					uploads = append(uploads, step.With["sarif_file"]+" "+step.With["category"]+" if "+step.If)
				}
			}
			if !reflect.DeepEqual(jobs[c.job].Permissions, c.permissions) || !reflect.DeepEqual(uploads, c.uploads) {
				t.Errorf("permissions %v, uploads %v", jobs[c.job].Permissions, uploads)
			}
		})
	}
}

func TestAScanWorkflowWithNoChannelScansOnlyImages(t *testing.T) {
	_, jobs := scanWorkflow(t, trivyFixture(t)[".github/workflows/scan.yaml"])
	if len(jobs) != 3 {
		t.Errorf("the jobs: %v", jobs)
	}
}
