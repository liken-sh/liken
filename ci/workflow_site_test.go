package main

import (
	"reflect"
	"strings"
	"testing"
)

// The site job downloads a coverage profile only from a job that ran,
// because a job that did not run uploaded nothing.
func TestTheSiteDownloadsCoverageOnlyFromAJobThatRan(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\nrun = \"m\"\ncoverage = [\"coverage.out\"]\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	want := "if: ${{ fromJSON(needs.plan.outputs.components)['app'].check && contains(fromJSON(needs.plan.outputs.components)['app'].jobs, 'go') }}\n        with:\n          name: coverage-app-go"
	if !strings.Contains(string(files[".github/workflows/ci.yaml"]), want) {
		t.Errorf("the site's download lacks %q", want)
	}
}

// The site job builds every manual with Hugo. It restores the Hugo
// build cache that the docs jobs save, saves its own cache on main when
// the restore found no exact key, and builds the manuals in parallel.
func TestTheSiteSharesTheHugoBuildCacheAndBuildsInParallel(t *testing.T) {
	files, err := Workflows(fluxTree(t), graphFixture(t))
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	text := string(files[".github/workflows/ci.yaml"])
	for _, want := range []string{
		"key: hugo-build-${{ runner.os }}-${{ hashFiles('**/docs/go.sum') }}\n" +
			"          restore-keys: hugo-build-${{ runner.os }}-\n",
		"      - if: ${{ github.ref == 'refs/heads/main' && steps.hugo-cache.outputs.cache-hit != 'true' }}\n        uses: " + pinnedText("actions/cache/save"),
		"run: make -j\"$(nproc)\" site SITE_URL=https://liken.sh\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the site job lacks %q", want)
		}
	}
}

// The site job reads only the tree and the coverage artifacts of the
// check jobs, so it waits for the checks and not for the publishes.
// The release record lists every published version, so it waits for
// the publishes as well.
func TestTheSiteWaitsForTheChecksAndTheRecordForThePublishes(t *testing.T) {
	root := writeTree(t, map[string]string{
		"lib/package.toml":   "[package]\nname = \"lib\"\n",
		"app/package.toml":   "[package]\nname = \"app\"\n[depends]\ncomponents = [\"lib\"]\n[outputs]\ndeploy = \"deploy\"\n",
		"liken/flux/VERSION": testFlux + "\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatalf("loading the components: %v", err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	jobs := workflowJobs(t, files[".github/workflows/ci.yaml"])
	cases := map[string][]any{
		"site":   {"plan", "lib", "app"},
		"record": {"plan", "lib", "app", "app-publish"},
	}
	for name, needs := range cases {
		if got := jobs[name]["needs"]; !reflect.DeepEqual(got, needs) {
			t.Errorf("%s needs %v, want %v", name, got, needs)
		}
	}
}

// deploy-pages lists the run's artifacts by name, and that listing can
// miss an artifact that the same job uploaded moments before. So the
// site job builds and uploads, and site-deploy deploys in a job of its
// own, with the Pages permissions and the concurrency group.
func TestTheSiteDeploysInAJobOfItsOwnAfterTheBuild(t *testing.T) {
	files, err := Workflows(fluxTree(t), graphFixture(t))
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	jobs := workflowJobs(t, files[".github/workflows/ci.yaml"])
	cases := []struct {
		job, field string
		want       any
	}{
		{"site", "permissions", map[string]any{"contents": "read"}},
		{"site-deploy", "needs", "site"},
		{"site-deploy", "permissions", map[string]any{"pages": "write", "id-token": "write"}},
		{"site-deploy", "concurrency", map[string]any{"group": "site"}},
	}
	for _, c := range cases {
		if got := jobs[c.job][c.field]; !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s has %s %v, want %v", c.job, c.field, got, c.want)
		}
	}
}

// A rerun of the failed deploy alone does not run the build again, so
// the deploy takes the artifact's name from the build job's output and
// not from its own attempt.
func TestTheSiteDeployTakesTheArtifactThatTheBuildUploaded(t *testing.T) {
	files, err := Workflows(fluxTree(t), graphFixture(t))
	if err != nil {
		t.Fatalf("generating the workflows: %v", err)
	}
	text := string(files[".github/workflows/ci.yaml"])
	for _, want := range []string{
		"    outputs:\n      artifact: github-pages-${{ github.run_attempt }}\n",
		"          name: github-pages-${{ github.run_attempt }}\n",
		"          artifact_name: ${{ needs.site.outputs.artifact }}\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the site jobs lack %q", want)
		}
	}
}
