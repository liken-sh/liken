package main

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// shardedJobs is the go and go-shard jobs of a component's workflow,
// as the YAML parser reads them.
type shardedJobs struct {
	Jobs map[string]struct {
		If       string `yaml:"if"`
		Needs    string `yaml:"needs"`
		Strategy struct {
			FailFast *bool `yaml:"fail-fast"`
			Matrix   struct {
				Shard []int `yaml:"shard"`
			} `yaml:"matrix"`
		} `yaml:"strategy"`
		Env   map[string]string `yaml:"env"`
		Steps []struct {
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func shardedTree(t *testing.T, job string) string {
	return writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[jobs]]\nname = \"go\"\ntoolchain = \"go\"\n" + job,
	})
}

// A job with shards runs its shard-run command on one runner for each
// shard, and then runs its own command on the shards' coverage files.
// The site and the plan know only the job's name, so the shards select
// on it, and the job uploads the merged file under its usual artifact.
func TestAShardedJobRunsItsShardsAndThenMergesThem(t *testing.T) {
	root := shardedTree(t, "run = \"make gate\"\nshards = 3\nshard-run = \"make shard\"\ncoverage = [\"coverage.out\"]\nenv = { CGO_ENABLED = \"1\" }\n")
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	var workflow shardedJobs
	if err := yaml.Unmarshal(files[".github/workflows/component-app.yaml"], &workflow); err != nil {
		t.Fatal(err)
	}
	shards, gate := workflow.Jobs["go-shard"], workflow.Jobs["go"]

	selects := "contains(fromJSON(inputs.jobs), 'go')"
	if !strings.Contains(shards.If, selects) || !strings.Contains(gate.If, selects) {
		t.Errorf("the conditions are %q and %q, and both should select the job go", shards.If, gate.If)
	}
	if got := shards.Strategy.Matrix.Shard; !reflect.DeepEqual(got, []int{1, 2, 3}) {
		t.Errorf("the shards are %v, not [1 2 3]", got)
	}
	if shards.Strategy.FailFast == nil || *shards.Strategy.FailFast {
		t.Error("a failed shard cancels the others")
	}
	if want := map[string]string{"SHARD": "${{ matrix.shard }}", "SHARDS": "3", "CGO_ENABLED": "1"}; !reflect.DeepEqual(shards.Env, want) {
		t.Errorf("the shards' env is %v, not %v", shards.Env, want)
	}
	if gate.Needs != "go-shard" {
		t.Errorf("the job needs %q, not the shards", gate.Needs)
	}
	if _, ok := gate.Env["SHARD"]; ok {
		t.Error("the job itself sets SHARD")
	}

	var shardRuns, gateRuns, shardUploads, gateUploads, downloads []string
	for _, step := range shards.Steps {
		if step.Run != "" {
			shardRuns = append(shardRuns, step.Run)
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			shardUploads = append(shardUploads, step.With["name"])
		}
	}
	for _, step := range gate.Steps {
		if step.Run != "" {
			gateRuns = append(gateRuns, step.Run)
		}
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") {
			gateUploads = append(gateUploads, step.With["name"])
		}
		if strings.HasPrefix(step.Uses, "actions/download-artifact@") {
			downloads = append(downloads, step.With["pattern"]+" -> "+step.With["path"])
		}
	}
	for _, c := range []struct {
		what      string
		got, want []string
	}{
		{"the shards run", shardRuns, []string{"make shard"}},
		{"the job runs", gateRuns, []string{"make gate"}},
		{"the shards upload", shardUploads, []string{"coverage-app-go-shard-${{ matrix.shard }}"}},
		{"the job uploads", gateUploads, []string{"coverage-app-go"}},
		{"the job downloads", downloads, []string{"coverage-app-go-shard-* -> app/coverage-shards"}},
	} {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s %q, not %q", c.what, c.got, c.want)
		}
	}
}

func TestAShardedJobIsDeclaredWhole(t *testing.T) {
	for _, c := range []struct {
		name, job, err string
	}{
		{"shards with no shard-run", "run = \"make gate\"\nshards = 2\ncoverage = [\"c.out\"]\n", "need a shard-run command"},
		{"a shard-run with no shards", "run = \"make gate\"\nshard-run = \"make shard\"\ncoverage = [\"c.out\"]\n", "need a shard-run command"},
		{"shards with no coverage", "run = \"make gate\"\nshards = 2\nshard-run = \"make shard\"\n", "needs coverage files"},
		{"a job named for the shards", "run = \"make gate\"\nshards = 2\nshard-run = \"make shard\"\ncoverage = [\"c.out\"]\n[[jobs]]\nname = \"go-shard\"\ntoolchain = \"go\"\nrun = \"make other\"\n", "takes the name of the shards"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := LoadComponents(shardedTree(t, c.job))
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("the error is %v, not one that says %q", err, c.err)
			}
		})
	}
}
