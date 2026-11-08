package main

import (
	"maps"
	"slices"
	"strings"
	"testing"
)

// fixtureBuildKit is the BuildKit image that a fixture's images.toml
// pins.
const fixtureBuildKit = "moby/buildkit:v0.34.2@sha256:1111111111111111111111111111111111111111111111111111111111111111"

// fixtureBuildKitImages is an images.toml that pins only BuildKit, for
// a fixture whose workflows build images.
const fixtureBuildKitImages = "[build]\nbuildkit = \"" + fixtureBuildKit + "\"\n"

// buildxStep is a workflow whose one job sets up buildx with the
// inputs that follow the step's uses: line.
func buildxStep(with string) string {
	return "jobs:\n  images:\n    steps:\n      - uses: " + pinnedText("docker/setup-buildx-action") + "\n" + with
}

func TestTheChecksRefuseABuilderThatNamesNoPinnedBuildKit(t *testing.T) {
	other := "moby/buildkit:v0.34.2@sha256:" + strings.Repeat("2", 64)
	cases := []struct {
		name, workflow string
		findings       int
	}{
		{"the pinned image", buildxStep("        with:\n          driver-opts: image=" + fixtureBuildKit + "\n"), 0},
		{"the pinned image among other options", buildxStep("        with:\n          driver-opts: |\n            network=host\n            image=" + fixtureBuildKit + "\n"), 0},
		{"no inputs", buildxStep(""), 1},
		{"driver-opts with no image", buildxStep("        with:\n          driver-opts: network=host\n"), 1},
		{"the image by its tag", buildxStep("        with:\n          driver-opts: image=moby/buildkit:v0.34.2\n"), 1},
		{"an image at another digest", buildxStep("        with:\n          driver-opts: image=" + other + "\n"), 1},
		{"a composite action with no image", "runs:\n  steps:\n    - uses: " + pinnedText("docker/setup-buildx-action") + "\n", 1},
		{"a step of another action", "jobs:\n  ci:\n    steps:\n      - uses: " + pinnedText("actions/checkout") + "\n", 0},
		{"a job that calls a reusable workflow", "jobs:\n  app:\n    uses: ./.github/workflows/component-app.yaml\n    with:\n      stage: check\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			findings, err := unpinnedBuilders("w.yaml", []byte(tc.workflow), fixtureBuildKit)
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != tc.findings {
				t.Errorf("findings %v, want %d", findings, tc.findings)
			}
		})
	}
}

func TestTheChecksReadTheBuildersOfEveryWorkflowAndCompositeAction(t *testing.T) {
	root := writeTree(t, map[string]string{
		"toolchains/images.toml":                 fixtureBuildKitImages,
		".github/workflows/pinned.yaml":          buildxStep("        with:\n          driver-opts: image=" + fixtureBuildKit + "\n"),
		".github/workflows/default.yaml":         buildxStep(""),
		".github/actions/build-setup/action.yml": "runs:\n  steps:\n    - uses: " + pinnedText("docker/setup-buildx-action") + "\n",
		"docs/example.yaml":                      buildxStep(""),
	})
	findings, err := checkBuildKit(root)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, f := range findings {
		files = append(files, strings.SplitN(f, ":", 2)[0])
	}
	want := []string{".github/actions/build-setup/action.yml", ".github/workflows/default.yaml"}
	if !slices.Equal(files, want) {
		t.Errorf("findings %v, want one in each of %v", findings, want)
	}
}

func TestEveryGeneratedBuilderRunsThePinnedBuildKit(t *testing.T) {
	root, components := everyJobFixture(t)
	files, err := Workflows(root, components)
	if err != nil {
		t.Fatal(err)
	}
	text := string(files[".github/workflows/component-app.yaml"])
	if got := strings.Count(text, "driver-opts: image="+fixtureBuildKit+"\n"); got != 2 {
		t.Errorf("the images job and the publish job name the pinned BuildKit %d times, want 2:\n%s", got, text)
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		findings, err := unpinnedBuilders(name, files[name], fixtureBuildKit)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range findings {
			t.Error(f)
		}
	}
}

func TestTheGeneratorRefusesImagesWithNoPinnedBuildKit(t *testing.T) {
	root := writeTree(t, map[string]string{
		"app/package.toml": "[package]\nname = \"app\"\n[[outputs.images]]\nname = \"app\"\ndescription = \"The app image.\"\n",
	})
	components, err := LoadComponents(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Workflows(root, components); err == nil || !strings.Contains(err.Error(), "names no buildkit image") {
		t.Errorf("got %v", err)
	}
}
