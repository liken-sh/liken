package main

// The on-board components record: how the image's build-time facts
// become status.version fields.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/liken-sh/liken/machine"
)

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
	// This record comes from a build that knows about a component
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
