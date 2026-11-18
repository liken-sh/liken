package main

import (
	"testing"
	"testing/synctest"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// biosCabinet is the SHA-256 of the cabinet that systemFirmware's
// releases have.
const biosCabinet = "9c085d03"

// approve sets a version and a pinned cabinet on the BIOS Firmware, as a
// person does, and limits the approval to the machines that a selector
// chooses.
func approve(t *testing.T, w *world, version, sha256 string, machines *metav1.LabelSelector) {
	t.Helper()
	f := w.api.firmware(t, biosObjectName)
	f.Spec.Update.Version = version
	f.Spec.Update.Cabinet = &pinnedCabinet{URL: "https://fwupd.org/downloads/" + version + ".cab", SHA256: sha256}
	f.Spec.Machines = machines
	w.api.putFirmware(f)
	w.settle()
}

// approved returns the Approved condition and the pending count of the
// BIOS Firmware.
func approved(t *testing.T, w *world) (condition, int) {
	t.Helper()
	f := w.api.firmware(t, biosObjectName)
	c, _ := conditions.Find(f.Status.Conditions, approvedCondition)
	return c, updateCountsOf(t, &f).Pending
}

func TestAnApprovedVersionThatACatalogOffersIsPending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		approve(t, w, "0.0.4.2", biosCabinet, nil)

		c, pending := approved(t, w)
		if c.Status != conditions.True || c.Reason != reasonOffered || c.ObservedGeneration != 2 {
			t.Errorf("Approved is %+v, want True with the reason Offered at generation 2", c)
		}
		if pending != 1 {
			t.Errorf("%d machines pending, want node-2", pending)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Normal", reasonOffered}) {
			t.Errorf("the operator posted %v, want a Normal Offered", got)
		}
	})
}

func TestAnApprovalOfAnotherCabinetIsNotApproved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		approve(t, w, "0.0.4.2", "beef", nil)

		c, pending := approved(t, w)
		if c.Status != conditions.False || c.Reason != reasonCabinetDiffers || pending != 0 {
			t.Errorf("Approved is %+v with %d pending, want False with the reason CabinetDiffers and none pending", c, pending)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Warning", reasonCabinetDiffers}) {
			t.Errorf("the operator posted %v, want a Warning CabinetDiffers", got)
		}
	})
}

func TestADeviceThatInstallsTheApprovedVersionKeepsTheApproval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		approve(t, w, "0.0.4.2", biosCabinet, nil)
		rescan(t, w, systemFirmware("0.0.4.2"), secureBootDBX("2"))

		c, pending := approved(t, w)
		if c.Status != conditions.True || c.Reason != reasonInstalled || pending != 0 {
			t.Errorf("Approved is %+v with %d pending, want True with the reason Installed and none pending", c, pending)
		}
	})
}

func TestAWithdrawnReleaseEndsTheApproval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		approve(t, w, "0.0.4.2", biosCabinet, nil)
		rescan(t, w, systemFirmware("0.0.3.5", "0.0.4.3"), secureBootDBX("2"))

		c, pending := approved(t, w)
		if c.Status != conditions.False || c.Reason != reasonNotOffered || pending != 0 {
			t.Errorf("Approved is %+v with %d pending, want False with the reason NotOffered and none pending", c, pending)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Warning", reasonNotOffered}) {
			t.Errorf("the operator posted %v, want a Warning NotOffered", got)
		}
	})
}

// labelMachine sets the labels of a Machine and keeps the status that
// the OS wrote.
func (a *fakeAPI) labelMachine(name string, labels map[string]string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[machinesPath][name])
	next["metadata"].(map[string]any)["labels"] = toMap(labels)
	a.store(machinesPath, next, "MODIFIED")
}

func TestAnApprovalCoversOnlyTheMachinesItSelects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		approve(t, w, "0.0.4.2", biosCabinet, &metav1.LabelSelector{MatchLabels: map[string]string{"canary": "true"}})

		c, pending := approved(t, w)
		if c.Reason != reasonNotOffered || pending != 0 {
			t.Errorf("before the label, Approved is %+v with %d pending, want NotOffered and none pending", c, pending)
		}

		w.api.labelMachine("node-2", map[string]string{"canary": "true"})
		w.settle()
		c, pending = approved(t, w)
		if c.Reason != reasonOffered || pending != 1 {
			t.Errorf("after the label, Approved is %+v with %d pending, want Offered and node-2 pending", c, pending)
		}
	})
}

func TestAnApprovalWithAnInvalidMachineSelectorIsNotApproved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		approve(t, w, "0.0.4.2", biosCabinet, &metav1.LabelSelector{MatchLabels: map[string]string{"not a key!": "x"}})

		if c, _ := approved(t, w); c.Status != conditions.False || c.Reason != reasonMachinesInvalid {
			t.Errorf("Approved is %+v, want False with the reason MachinesInvalid", c)
		}
	})
}

func TestRemovingTheVersionRemovesTheApproval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		approve(t, w, "0.0.4.2", biosCabinet, nil)
		f := w.api.firmware(t, biosObjectName)
		f.Spec.Update.Version, f.Spec.Update.Cabinet = "", nil
		w.api.putFirmware(f)
		w.settle()

		if _, found := conditions.Find(w.api.firmware(t, biosObjectName).Status.Conditions, approvedCondition); found {
			t.Error("the Firmware has an Approved condition with no spec.update.version, want none")
		}
	})
}

func containsPosted(events []posted, want posted) bool {
	for _, e := range events {
		if e == want {
			return true
		}
	}
	return false
}
