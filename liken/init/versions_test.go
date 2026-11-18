package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/liken/machine"
)

// The tests read the image build from the repository, not from a
// built image, so they run on any checkout.
const buildScript = "../image/build.sh"

// componentsLoop matches the line of the image build that lists every
// component in the record.
var componentsLoop = regexp.MustCompile(`(?m)^\s*for component in ([^;]+); do$`)

func recordedComponents(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(buildScript)
	if err != nil {
		t.Fatalf("reading the image build script: %v", err)
	}
	match := componentsLoop.FindSubmatch(raw)
	if match == nil {
		t.Fatalf("%s has no `for component in ...` line, which the test reads to list the components of the record", buildScript)
	}
	return strings.Fields(string(match[1]))
}

// versionFields returns every field of a VersionStatus, so a test can
// find a version without knowing which field holds it.
func versionFields(v machine.VersionStatus) []string {
	value := reflect.ValueOf(v)
	fields := make([]string, 0, value.NumField())
	for i := range value.NumField() {
		fields = append(fields, value.Field(i).String())
	}
	return fields
}

func TestEveryRecordedComponentReachesTheVersionStatus(t *testing.T) {
	// Each component gets a distinct version, so each must appear in
	// some field.
	names := recordedComponents(t)
	var record strings.Builder
	record.WriteString("components:\n")
	for _, name := range names {
		fmt.Fprintf(&record, "  - name: %s\n    version: pin-%s\n", name, name)
	}
	componentsFile(t, record.String())

	v := machine.VersionStatus{}
	applyComponentFacts(&v)
	arrived := versionFields(v)

	for _, name := range names {
		if slices.Contains(observedAtRuntime, name) {
			continue
		}
		if slices.Contains(arrived, "pin-"+name) {
			continue
		}
		t.Errorf("the image build writes %[1]q to the components record, and no status.version field reports it. "+
			"Add a field to machine.VersionStatus, a case for %[1]q to applyComponentFacts, the matching "+
			"writeFact and readFact lines in machine/factswrite.go and machine/factsread.go, and the property "+
			"under status.version in machine/manifests/machines-crd.yaml. If init reads the version of %[1]q "+
			"from the running machine instead, add the name to observedAtRuntime in init/versions.go.", name)
	}
}

func TestObservedComponentsAreNamesTheRecordHolds(t *testing.T) {
	// An entry that the build does not write would exempt nothing,
	// and it would hide the next component of that name that needs a
	// field.
	names := recordedComponents(t)
	for _, name := range observedAtRuntime {
		if name == "liken" {
			continue // liken is the machine's own version, not a vendored one
		}
		if !slices.Contains(names, name) {
			t.Errorf("observedAtRuntime lists %q, which the image build does not write to the components record, want only names that it writes", name)
		}
	}
}

// componentsFile writes a components record and points
// componentsPath at it.
func componentsFile(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "components.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writing the components record: %v", err)
	}
	orig := componentsPath
	componentsPath = path
	t.Cleanup(func() { componentsPath = orig })
}

func TestComponentFactsFillTheVersionBlock(t *testing.T) {
	componentsFile(t, `
components:
  - name: k3s
    version: v1.36.2+k3s1
  - name: trust
    version: 2026-05-14
  - name: e2fsprogs
    version: 1.47.1
  - name: open-iscsi
    version: 2.1.11
  - name: nfs-utils
    version: 2.8.3
  - name: systemd-boot
    version: 259.5-0ubuntu3
  - name: grub
    version: 2.12-1ubuntu7.3
  - name: hwdata
    version: v0.409
  - name: tzdata
    version: 2026c
`)

	v := machine.VersionStatus{Liken: "2026.07.18-002"}
	applyComponentFacts(&v)

	want := machine.VersionStatus{
		Liken:       "2026.07.18-002",
		K3s:         "v1.36.2+k3s1",
		Trust:       "2026-05-14",
		E2fsprogs:   "1.47.1",
		OpenISCSI:   "2.1.11",
		NFSUtils:    "2.8.3",
		SystemdBoot: "259.5-0ubuntu3",
		Grub:        "2.12-1ubuntu7.3",
		Hwdata:      "v0.409",
		Tzdata:      "2026c",
	}
	if v != want {
		t.Errorf("version = %+v, want %+v", v, want)
	}
}

func TestComponentFactsNeverOverrideObservedFacts(t *testing.T) {
	// The machine reads the kernel version from uname and the
	// iptables version from iptables -V, not from the record.
	componentsFile(t, `
components:
  - name: kernel
    version: 7.1.2
  - name: xtables
    version: v0.15.2
  - name: liken
    version: 9999.99.99-999
  - name: k3s
    version: v1.36.2+k3s1
`)

	v := machine.VersionStatus{
		Liken:   "2026.07.18-002",
		Kernel:  "7.1.2-070102-generic",
		Xtables: "v1.8.11 (legacy)",
	}
	applyComponentFacts(&v)

	if v.Kernel != "7.1.2-070102-generic" || v.Xtables != "v1.8.11 (legacy)" || v.Liken != "2026.07.18-002" {
		t.Errorf("version = %+v, want kernel, xtables, and liken unchanged by the record", v)
	}
	if v.K3s != "v1.36.2+k3s1" {
		t.Errorf("k3s = %q, want v1.36.2+k3s1", v.K3s)
	}
}

func TestComponentFactsTolerateAMissingRecord(t *testing.T) {
	orig := componentsPath
	componentsPath = filepath.Join(t.TempDir(), "absent.yaml")
	t.Cleanup(func() { componentsPath = orig })

	v := machine.VersionStatus{Liken: "dev"}
	applyComponentFacts(&v)

	if v.Liken != "dev" || v.K3s != "" {
		t.Errorf("version = %+v, want untouched", v)
	}
}

func TestComponentFactsIgnoreUnknownComponents(t *testing.T) {
	componentsFile(t, `
components:
  - name: quantum-flux
    version: 1.0.0
  - name: grub
    version: 2.12-1ubuntu7.3
`)

	v := machine.VersionStatus{}
	applyComponentFacts(&v)

	if v.Grub != "2.12-1ubuntu7.3" {
		t.Errorf("grub = %q, want 2.12-1ubuntu7.3", v.Grub)
	}
}
