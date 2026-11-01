package main

import (
	"strings"
	"testing"
)

// An image or a release whose build compiles a toolchain from source
// takes longer than the default limits of the images and publish jobs,
// so the component raises them, and the jobs keep the defaults
// otherwise.
func TestABuildThatNeedsMoreTimeRaisesItsJobs(t *testing.T) {
	cases := map[string]struct {
		component, toml, images, publish string
	}{
		"the defaults": {"app",
			"[package]\nname = \"app\"\n[[outputs.images]]\nname = \"app\"\n",
			"    timeout-minutes: 45\n", "    timeout-minutes: 30\n"},
		"an image that names a timeout": {"app",
			"[package]\nname = \"app\"\n[[outputs.images]]\nname = \"app\"\n[[outputs.images]]\nname = \"daemons\"\ntimeout = 100\n",
			"    timeout-minutes: 100\n", "    timeout-minutes: 100\n"},
		"a channel that names a timeout": {"os",
			"[package]\nname = \"os\"\n[outputs]\nchannel = true\ntimeout = 90\n",
			"", "    timeout-minutes: 90\n"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeTree(t, map[string]string{c.component + "/package.toml": c.toml})
			components, err := LoadComponents(root)
			if err != nil {
				t.Fatal(err)
			}
			files, err := Workflows(root, components)
			if err != nil {
				t.Fatal(err)
			}
			text := string(files[".github/workflows/component-"+c.component+".yaml"])
			images := "  images:\n    name: ${{ matrix.image }}\n    if: ${{ inputs.stage == 'check' && fromJSON(inputs.images)[0] != null }}\n    runs-on: ubuntu-26.04\n"
			if c.images != "" && !strings.Contains(text, images+c.images) {
				t.Errorf("the images job lacks %q:\n%s", c.images, text)
			}
			publish := text[strings.Index(text, "  publish:\n"):]
			if !strings.Contains(publish, "    runs-on: ubuntu-26.04\n"+c.publish) {
				t.Errorf("the publish job lacks %q:\n%s", c.publish, publish)
			}
		})
	}
}
