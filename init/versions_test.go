package main

// The on-board components record: how the image's build-time facts
// become status.version fields.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/liken-sh/liken/machine"
)

// buildScript is the image build, read from the repository rather than
// from a built image, so the guard below runs on any checkout.
const buildScript = "../image/build.sh"

// componentsLoop finds the one line of the image build that lists every
// component the record names. That list is the authority: a person who
// vendors a new component adds its name there, and the guard below
// fails until the fold reports it.
var componentsLoop = regexp.MustCompile(`(?m)^\s*for component in ([^;]+); do$`)

// recordedComponents reads the component names out of the image build.
func recordedComponents(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(buildScript)
	if err != nil {
		t.Fatal(err)
	}
	match := componentsLoop.FindSubmatch(raw)
	if match == nil {
		t.Fatalf("%s no longer holds a `for component in ...` list; the guard below reads that list as the authority on what the record names", buildScript)
	}
	return strings.Fields(string(match[1]))
}

// versionFields reports every string in a VersionStatus, so the guard
// can ask whether a component's version landed anywhere at all without
// naming the field it should have landed in.
func versionFields(v machine.VersionStatus) []string {
	value := reflect.ValueOf(v)
	fields := make([]string, 0, value.NumField())
	for i := range value.NumField() {
		fields = append(fields, value.Field(i).String())
	}
	return fields
}

func TestEveryRecordedComponentReachesTheVersionStatus(t *testing.T) {
	// This is the mechanical half of the rule that every vendored
	// component reports its version in Machine status. It feeds the
	// fold a record holding every name the image build writes, each
	// with a version of its own, and then checks that each version
	// arrived somewhere in the block.
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
		t.Errorf("the image build ships %[1]q in the components record, and no status.version field reports it. "+
			"Add a field to machine.VersionStatus, a case for %[1]q to applyComponentFacts, the matching "+
			"writeFact and readFact lines in machine/factswrite.go and machine/factsread.go, and the property "+
			"under status.version in machine/manifests/machines-crd.yaml. If the running machine reports %[1]q "+
			"in its own vocabulary instead, add the name to observedAtRuntime in init/versions.go.", name)
	}
}

func TestObservedComponentsAreNamesTheRecordHolds(t *testing.T) {
	// The skip list must name real components. A stale entry there
	// would silently exempt nothing, and would hide the next
	// component that needs a field.
	names := recordedComponents(t)
	for _, name := range observedAtRuntime {
		if name == "liken" {
			continue // liken is the machine's own version, not a vendored one
		}
		if !slices.Contains(names, name) {
			t.Errorf("observedAtRuntime names %q, which the image build no longer ships in the components record", name)
		}
	}
}

// componentsFile writes a components record in the same form that
// image/build.sh stages one. It also points componentsPath at the
// new file for the test.
func componentsFile(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "components.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
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
	// The record also carries kernel and xtables pins, because the
	// release document lists every component. But the running
	// machine reports those itself, in its own vocabulary: uname's
	// release string and iptables' version-and-variant, not the
	// vendor pins.
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
		t.Errorf("observed facts were overridden: %+v", v)
	}
	if v.K3s != "v1.36.2+k3s1" {
		t.Errorf("k3s = %q", v.K3s)
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
	// This record comes from a build that names a component
	// this init does not recognize. The known fields still fill in,
	// and the unrecognized component is simply not reported.
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
		t.Errorf("grub = %q", v.Grub)
	}
}
