package main

import (
	"strings"
	"testing"
)

// signFixture is a pinned base with images, a component that publishes
// only a deploy artifact, and the OS, which publishes to the channel.
func signFixture(t *testing.T) map[string][]byte {
	t.Helper()
	root := writeTree(t, map[string]string{
		"base/package.toml":  "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\ndescription = \"The base image.\"\n",
		"app/package.toml":   "[package]\nname = \"app\"\n[outputs]\ndeploy = \"deploy\"\n",
		"os/package.toml":    "[package]\nname = \"os\"\n[outputs]\nchannel = true\n",
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

// id-token: write lets a job ask GitHub for the OIDC token that cosign
// signs with, so only a publish job that signs holds it: the publish of
// a component that pushes images, and the OS's publish to the channel.
// GitHub checks every job of a called workflow against the call,
// including a job that the call skips, so both calls of that
// component's workflow grant it.
func TestOnlyAPublishThatSignsIsGrantedIDToken(t *testing.T) {
	root := workflowJobs(t, signFixture(t)[".github/workflows/ci.yaml"])
	for name, want := range map[string]any{
		"base": "write", "base-publish": "write",
		"app": nil, "app-publish": nil,
		"os": "write", "os-publish": "write",
		"plan": nil,
	} {
		permissions, _ := root[name]["permissions"].(map[string]any)
		if got := permissions["id-token"]; got != want {
			t.Errorf("ci.yaml: %s grants id-token %v, want %v", name, got, want)
		}
	}
	for file, jobs := range map[string]map[string]any{
		".github/workflows/component-base.yaml": {"publish": "write", "images": nil},
		".github/workflows/component-app.yaml":  {"publish": nil},
		".github/workflows/component-os.yaml":   {"publish": "write"},
	} {
		parsed := workflowJobs(t, signFixture(t)[file])
		for name, want := range jobs {
			permissions, _ := parsed[name]["permissions"].(map[string]any)
			if got := permissions["id-token"]; got != want {
				t.Errorf("%s: %s grants id-token %v, want %v", file, name, got, want)
			}
		}
	}
}

// The OS's publish job signs each file of the release before it
// uploads the release, so every file goes up with its bundle beside
// it. A dry run uploads nothing and signs nothing.
func TestTheChannelPublishSignsBeforeItUploads(t *testing.T) {
	jobs := workflowJobs(t, signFixture(t)[".github/workflows/component-os.yaml"])
	steps, _ := jobs["publish"]["steps"].([]any)
	sign, upload := -1, -1
	for i, s := range steps {
		step, _ := s.(map[string]any)
		run, _ := step["run"].(string)
		switch {
		case strings.Contains(run, "releases/sign.sh"):
			sign = i
			if step["if"] != "${{ !inputs.dryrun }}" {
				t.Errorf("the signing step runs if %v, want only outside a dry run", step["if"])
			}
		case strings.Contains(run, "releases/publish.sh") && !strings.Contains(run, "bash -n"):
			upload = i
		}
	}
	if sign < 0 || upload < 0 || sign > upload {
		t.Errorf("the publish signs at step %d and uploads at step %d", sign, upload)
	}
	permissions, _ := jobs["publish"]["permissions"].(map[string]any)
	if permissions["contents"] != "read" {
		t.Errorf("the channel publish reads contents %v, want read for the checkout", permissions["contents"])
	}
}

// The OS's publish job builds the release, and on main it pushes the
// userland that it compiles to ghcr.io/liken-sh/userland, so its
// token must write packages like the build job's.
func TestTheChannelPublishCanPushTheUserland(t *testing.T) {
	jobs := workflowJobs(t, signFixture(t)[".github/workflows/component-os.yaml"])
	permissions, _ := jobs["publish"]["permissions"].(map[string]any)
	if permissions["packages"] != "write" {
		t.Errorf("the channel publish grants packages %v, want write", permissions["packages"])
	}
}
