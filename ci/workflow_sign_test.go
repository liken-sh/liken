package main

import "testing"

// signFixture is a pinned base with images, a component that publishes
// only a deploy artifact, and the OS, which publishes to the channel.
func signFixture(t *testing.T) map[string][]byte {
	t.Helper()
	root := writeTree(t, map[string]string{
		"base/package.toml":  "[package]\nname = \"base\"\nversion = \"20260928\"\nrevision = 1\n[[outputs.images]]\nname = \"base\"\n",
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
// signs with, so only the publish job of a component that pushes
// images holds it. GitHub checks every job of a called workflow
// against the call, including a job that the call skips, so both calls
// of that component's workflow grant it.
func TestOnlyThePublishOfImagesIsGrantedIDToken(t *testing.T) {
	root := workflowJobs(t, signFixture(t)[".github/workflows/ci.yaml"])
	for name, want := range map[string]any{
		"base": "write", "base-publish": "write",
		"app": nil, "app-publish": nil,
		"os": nil, "os-publish": nil,
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
		".github/workflows/component-os.yaml":   {"publish": nil},
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
