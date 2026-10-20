package main

// These tests cover which rule runs on which device: two rules on one
// device, a selector that does not compile, a selector that fails on
// one device, and spec.machines.

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Two rules that match one device run neither, and each reports the
// conflict and names the other. When one rule goes, the other runs.
func TestTwoRulesOnOneDeviceConflict(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putFirmware(qhy5lii())
		w.api.putFirmware(named(qhy5lii(), "qhy-fx2"))
		// Both rules reach the operator's store before the device
		// appears. A rule that arrives after an upload started leaves
		// that upload to finish.
		w.settle()
		w.api.publish("liken-1", bareChip("1-4.1.2", 12))
		w.settle()

		if got := w.api.created(uploadPodsPath); got != 0 {
			t.Errorf("the operator created %d upload pods, want none", got)
		}
		for rule, other := range map[string]string{"qhy5l-ii": "qhy-fx2", "qhy-fx2": "qhy5l-ii"} {
			e := w.entry(t, rule)
			if e.Result != resultConflict || !strings.Contains(e.Message, other) {
				t.Errorf("the entry of %s is %+v, want Conflict naming %s", rule, e, other)
			}
			if got := w.api.firmware(t, rule).Status.Counts; got != (counts{Conflict: 1}) {
				t.Errorf("status.counts of %s = %+v, want one conflict", rule, got)
			}
			want := []posted{{"Normal", reasonCompiled}, {"Warning", reasonConflict}}
			if got := w.postedAbout(rule); !slices.Equal(got, want) {
				t.Errorf("the Events of %s are %v, want %v", rule, got, want)
			}
		}

		w.api.deleteObject(firmwaresPath, "qhy-fx2")
		w.settle()
		if e := w.entry(t, "qhy5l-ii"); e.Result != resultRunning {
			t.Errorf("after the other rule went, the entry is %+v, want Running", e)
		}
	})
}

// A rule that matches no device writes each count as 0, so the printer
// columns of kubectl get show 0 and not an empty cell.
func TestARuleWithNoDevicesWritesZeroCounts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putFirmware(qhy5lii())
		w.settle()

		w.api.mu.Lock()
		status := clone(w.api.objects[firmwaresPath]["qhy5l-ii"])["status"].(map[string]any)
		w.api.mu.Unlock()
		want := map[string]any{"running": 0.0, "loaded": 0.0, "failed": 0.0, "conflict": 0.0}
		if got, _ := status["counts"].(map[string]any); !maps.Equal(got, want) {
			t.Errorf("status.counts = %v, want %v", status["counts"], want)
		}
	})
}

// A selector that does not compile sets Valid to False with the
// compiler's message, and the rule runs nowhere. A fix sets it True.
func TestASelectorThatDoesNotCompileIsInvalid(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		broken := qhy5lii()
		broken.Spec.Selector = `device.attributes["liken.sh"].vendor ==`
		w.api.putFirmware(broken)
		w.api.publish("liken-1", bareChip("1-4.1.2", 12))
		w.settle()

		got := w.api.firmware(t, "qhy5l-ii").Status
		if v := got.valid(); v.Status != "False" || v.Reason != reasonSelectorInvalid || v.Message == "" || v.ObservedGeneration != 1 {
			t.Errorf("Valid = %+v, want False, %s, with the compiler's message", v, reasonSelectorInvalid)
		}
		if n := w.api.created(uploadPodsPath); n != 0 {
			t.Errorf("the operator created %d upload pods, want none", n)
		}

		w.api.putFirmware(qhy5lii())
		w.settle()
		if v := w.api.firmware(t, "qhy5l-ii").Status.valid(); v.Status != "True" || v.Reason != reasonCompiled || v.ObservedGeneration != 2 {
			t.Errorf("after the fix Valid = %+v, want True, %s, at generation 2", v, reasonCompiled)
		}
		want := []posted{{"Warning", reasonSelectorInvalid}, {"Normal", reasonCompiled}, {"Normal", reasonUploadStarted}}
		if got := w.postedAbout("qhy5l-ii"); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}

// A selector that fails on one device, such as one that reads an
// attribute the device lacks with no has(), counts as no match there.
func TestASelectorThatFailsOnADeviceSkipsIt(t *testing.T) {
	selector, err := newSelectors().compile(`device.attributes["liken.sh"].serial == "abc"`)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		device usb
		want   bool
	}{
		{"no serial attribute", bareChip("1-4", 3), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := devicesOf(sliceOf("liken-1", c.device))[0]
			if got := matches(t.Context(), "serial", selector, d); got != c.want {
				t.Errorf("matches = %v, want %v", got, c.want)
			}
		})
	}
}

// spec.machines limits a rule to the machines whose labels it selects.
// A machine with no Machine object has no labels, so a rule with
// spec.machines does not run there.
func TestSpecMachinesLimitsTheRule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putMachine("liken-1", map[string]string{"observatory": "true"})
		w.api.putMachine("stick-1", nil)
		rule := qhy5lii()
		rule.Spec.Machines = &metav1.LabelSelector{MatchLabels: map[string]string{"observatory": "true"}}
		w.api.putFirmware(rule)
		w.api.publish("liken-1", bareChip("1-4.1.2", 12))
		w.api.publish("stick-1", bareChip("1-2", 5))
		w.api.publish("node-9", bareChip("1-2", 7))
		w.settle()

		uploads := w.api.uploads(t)
		if len(uploads) != 1 || uploads[0].Spec.NodeSelector["kubernetes.io/hostname"] != "liken-1" {
			t.Errorf("the upload pods are %+v, want one on liken-1", uploads)
		}
	})
}

// A label selector that does not parse sets Valid to False.
func TestSpecMachinesThatDoesNotParseIsInvalid(t *testing.T) {
	_, err := filterMachines(&metav1.LabelSelector{MatchExpressions: []metav1.LabelSelectorRequirement{{Key: "a", Operator: "Near"}}}, nil)
	if v := validity(err); v.Status != "False" || v.Reason != reasonSelectorInvalid {
		t.Errorf("Valid = %+v, want False, %s", v, reasonSelectorInvalid)
	}
}

// Only a device with an address, a bus number, and a device number has
// a usbfs node to write to.
func TestOnlyADeviceWithAUSBNodeIsReachable(t *testing.T) {
	gpu := sliceOf("liken-1")
	gpu.Spec.Devices = []sliceDevice{{Name: "pci-0000-00-02-0"}}
	cases := []struct {
		name  string
		slice resourceSlice
		want  int
	}{
		{"a USB device", sliceOf("liken-1", bareChip("1-4", 3)), 1},
		{"a PCI device", gpu, 0},
		{"a slice with no node", sliceOf("", bareChip("1-4", 3)), 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := len(devicesOf(c.slice)); got != c.want {
				t.Errorf("%d devices, want %d", got, c.want)
			}
		})
	}
}

// A Machine that gains the label spec.machines selects gets the upload
// for the device it already has.
func TestAMachineThatGainsTheLabelGetsTheUpload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.api.putMachine("liken-1", nil)
		rule := qhy5lii()
		rule.Spec.Machines = &metav1.LabelSelector{MatchLabels: map[string]string{"observatory": "true"}}
		w.api.putFirmware(rule)
		w.api.publish("liken-1", bareChip("1-4.1.2", 12))
		w.settle()
		if got := w.api.created(uploadPodsPath); got != 0 {
			t.Fatalf("before the label the operator created %d upload pods, want none", got)
		}
		w.api.putMachine("liken-1", map[string]string{"observatory": "true"})
		w.settle()

		if got := w.api.created(uploadPodsPath); got != 1 {
			t.Errorf("after the label the operator created %d upload pods, want 1", got)
		}
	})
}
