package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// deployment lays out a manifests directory the way a real deployment
// carries one: machines/*.yaml under a common root.
func deployment(t *testing.T, manifests map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	machines := filepath.Join(dir, "machines")
	if err := os.Mkdir(machines, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range manifests {
		if err := os.WriteFile(filepath.Join(machines, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func manifestWithModules(name string, modules string) string {
	return `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: ` + name + `
spec:
  modules: ` + modules + `
`
}

func TestModulesAreUnionedSortedAndDeduplicated(t *testing.T) {
	dir := deployment(t, map[string]string{
		"node-1.yaml": manifestWithModules("node-1", "[v4l2loopback, nvidia]"),
		"node-2.yaml": manifestWithModules("node-2", "[nvidia, zram]"),
	})
	modules, err := declaredModules(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(modules, " ")
	if got != "nvidia v4l2loopback zram" {
		t.Errorf("modules: got %q", got)
	}
}

func TestMachinesWithNoModulesProduceNothing(t *testing.T) {
	dir := deployment(t, map[string]string{
		"node-1.yaml": `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
`,
	})
	modules, err := declaredModules(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 0 {
		t.Errorf("modules: got %v", modules)
	}
}

func TestMissingManifestsDirectoryIsEmptyNotAnError(t *testing.T) {
	modules, err := declaredModules(filepath.Join(t.TempDir(), "no-such-deployment"))
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 0 {
		t.Errorf("modules: got %v", modules)
	}
}

func TestMisspelledFieldFailsTheBuild(t *testing.T) {
	dir := deployment(t, map[string]string{
		"node-1.yaml": `
apiVersion: liken.sh/v1alpha1
kind: Machine
metadata:
  name: node-1
spec:
  modulez: [nvidia]
`,
	})
	if _, err := declaredModules(dir); err == nil {
		t.Fatal("expected the misspelled field to be an error")
	}
}

func TestRunPrintsOneModulePerLine(t *testing.T) {
	dir := deployment(t, map[string]string{
		"node-1.yaml": manifestWithModules("node-1", "[zram, nvidia]"),
	})
	var out bytes.Buffer
	if err := run([]string{"modules", dir}, &out); err != nil {
		t.Fatal(err)
	}
	if out.String() != "nvidia\nzram\n" {
		t.Errorf("output: got %q", out.String())
	}
}

func TestRunRejectsUnknownQuestions(t *testing.T) {
	if err := run([]string{"features", t.TempDir()}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected an error for a question inventory can't answer yet")
	}
}

func TestRunRequiresAQuestionAndADirectory(t *testing.T) {
	if err := run([]string{"modules"}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected a usage error")
	}
}
