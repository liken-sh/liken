package main

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A passed capsule rehearsal records the capsule's writes and the ESP's
// free space. It records no Append, which only a Secure Boot database
// update has.
func TestAPassedCapsuleRehearsalRecordsItsWrites(t *testing.T) {
	writes := []capsuleWrite{
		{Kind: "file", Name: "EFI/debian/fwupdx64.efi", Bytes: 69176, SHA256: "aa"},
		{Kind: "variable", Name: "BootNext-8be4df61-93ca-11d2-aa0d-00e098032b8c", Bytes: 6, SHA256: "bb"},
	}
	r := rehearsalResult{Result: resultPassed, Message: "the install stages a capsule", Bytes: 69176, SHA256: "cc",
		Free: 4096, ESPFree: 1 << 20, Writes: writes}

	got := recordOfRehearsal(rehearsalAttempt{machine: "node-5"}, r, time.Unix(0, 0))

	if got.Append != nil || got.Capsule == nil || !slices.Equal(got.Capsule.Writes, writes) ||
		got.Capsule.SHA256 != "cc" || got.Capsule.Bytes != 69176 || got.Capsule.ESPFree != 1<<20 {
		t.Errorf("the record is %+v with capsule %+v, want the writes and no append", got, got.Capsule)
	}
}

// A rehearsal pod mounts the next boot's ESP read-only, through the
// link that `liken`'s init writes, only when the machine has that link.
func TestARehearsalPodMountsTheNextBootsESP(t *testing.T) {
	c := trustedCatalog()
	c.Spec.Trust = nil
	f := &firmware{Metadata: firmwareMeta{Name: "lab-fmp"}}
	for _, nextESP := range []bool{true, false} {
		p := buildRehearsalPod(f, rehearsalAttempt{firmware: "lab-fmp", machine: "node-5"}, c, "http://cabinet", testScanImage, "liken-system", nextESP)

		v := slices.IndexFunc(p.Spec.Volumes, func(v volume) bool { return v.Name == nextESPVolume })
		mounted := slices.Contains(p.Spec.Containers[0].VolumeMounts, volumeMount{Name: nextESPVolume, MountPath: nextESPPath, ReadOnly: true})
		if (v >= 0) != nextESP || mounted != nextESP {
			t.Errorf("with an ESP %v, the pod has the volume %v and the mount %v", nextESP, v >= 0, mounted)
		}
		if v >= 0 && *p.Spec.Volumes[v].HostPath != (hostPath{Path: provenESPLink, Type: "Directory"}) {
			t.Errorf("the volume is %+v, want the link %s", p.Spec.Volumes[v].HostPath, provenESPLink)
		}
	}
}

func TestTheSchemaKeepsACapsulesRehearsal(t *testing.T) {
	f := firmware{APIVersion: firmwareAPIVersion, Kind: firmwareKind, Metadata: firmwareMeta{Name: "lab-fmp"},
		Spec: firmwareSpec{Update: &update{Component: "sh.liken.lab.fmp.firmware", Version: "2",
			Cabinet: &pinnedCabinet{URL: "http://10.0.2.2:8017/firmware/labfmp-v2.cab", SHA256: strings.Repeat("c", 64)}}}}
	r := rehearsalResult{Result: resultPassed, Message: "the install stages a capsule", Bytes: 2959, SHA256: "dd",
		Free: 4096, ESPFree: 1 << 20, Writes: []capsuleWrite{{Kind: "file", Name: "EFI/debian/fwupdx64.efi", Bytes: 69176, SHA256: "aa"}}}
	f.Status.Rehearsals = []rehearsal{recordOfRehearsal(rehearsalAttempt{machine: "node-5", boot: "2026-10-22T20:00:00Z"}, r, time.Unix(0, 0))}

	assertTheSchemaKeeps(t, firmwareCRD, f)
}

// keepBootOrder sets status.firmware.bootOrderKept on a Machine, or
// removes status.firmware when kept is nil. It keeps the rest of the
// Machine's status.
func (a *fakeAPI) keepBootOrder(name string, kept *bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object := clone(a.objects[machinesPath][name])
	status, _ := object["status"].(map[string]any)
	if status == nil {
		status = map[string]any{}
	}
	if kept == nil {
		delete(status, "firmware")
	} else {
		status["firmware"] = map[string]any{"bootOrderKept": *kept}
	}
	object["status"] = status
	a.store(machinesPath, object, "MODIFIED")
}

// passedCapsule is a rehearsal of the BIOS's capsule that passed.
var passedCapsule = rehearsalResult{Result: resultPassed, Message: "the install stages a capsule with 3 variables and 2 files",
	Bytes: 72135, SHA256: "ee", Free: 4096, ESPFree: 1 << 24,
	Writes: []capsuleWrite{{Kind: "file", Name: "EFI/debian/fwupdx64.efi", Bytes: 69176, SHA256: "aa"}}}

// capsuleWorld is approvedWorld after the BIOS's capsule passed its
// rehearsal on node-2, with node-2's bootOrderKept set to kept.
func capsuleWorld(t *testing.T, kept *bool) *world {
	t.Helper()
	w := approvedWorld(t)
	w.api.keepBootOrder("node-2", kept)
	w.settle()
	w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded, rehearsalLog(passedCapsule))
	w.settle()
	return w
}

// After fwupd's updater runs, the machine boots from the first entry of
// BootOrder. So the operator refuses to apply a capsule on a machine
// whose firmware did not keep BootOrder through its last reset.
func TestACapsuleAppliesOnlyWhereTheFirmwareKeepsBootOrder(t *testing.T) {
	no := false
	for _, kept := range []*bool{nil, &no} {
		synctest.Test(t, func(t *testing.T) {
			w := capsuleWorld(t, kept)

			if got := len(w.api.applies(t)); got != 0 {
				t.Errorf("%d apply pods, want none", got)
			}
			if r := w.onlyApplyRecord(t); r.Result != resultRefused || !strings.Contains(r.Message, "bootOrderKept") {
				t.Errorf("the record is %+v, want Refused for bootOrderKept", r)
			}
		})
	}
}

// The apply pod of a capsule mounts the next boot's ESP read-write,
// because fwupd stages the updater and the capsule there.
func TestACapsulesApplyPodMountsTheNextBootsESP(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		yes := true
		w := capsuleWorld(t, &yes)

		p := w.onlyApply(t)
		v := slices.IndexFunc(p.Spec.Volumes, func(v volume) bool { return v.Name == nextESPVolume })
		if v < 0 || p.Spec.Volumes[v].HostPath.Path != provenESPLink ||
			!slices.Contains(p.Spec.Containers[0].VolumeMounts, volumeMount{Name: nextESPVolume, MountPath: nextESPPath}) {
			t.Errorf("the apply pod's volumes are %+v, want the next boot's ESP mounted read-write", p.Spec.Volumes)
		}
		if r := w.onlyApplyRecord(t); !r.Capsule || r.SHA256 != "ee" {
			t.Errorf("the record is %+v, want a capsule's with the rehearsal's digest", r)
		}
	})
}

// The reboot after a staged capsule must start fwupd's updater, so the
// operator removes spec.hold and sets spec.keepBootNext on the
// Disruption. When the pod could not confirm that it staged the
// capsule, keepBootNext stays false, the reboot uses the BootNext that
// `liken` sets, and the capsule does not run.
func TestACapsulesTurnKeepsBootNextOnlyWhenItWasStaged(t *testing.T) {
	cases := []struct {
		name   string
		result applyResult
		keep   bool
	}{
		{"staged", applyResult{Result: resultWritten, Message: "staged the capsule"}, true},
		{"uncertain", applyResult{Result: resultUncertain, Message: "fwupd wrote other bytes than the rehearsal"}, false},
	}
	for _, c := range cases {
		synctest.Test(t, func(t *testing.T) {
			yes := true
			w := capsuleWorld(t, &yes)
			w.api.runPod(t, w.onlyApply(t).Metadata.Name, false)
			w.settle()
			w.api.readyPod(t, w.onlyApply(t).Metadata.Name)
			w.settle()
			w.api.holdTurn(t, turnName)
			w.settle()
			w.api.finishPod(t, w.onlyApply(t).Metadata.Name, applyContainer, podSucceeded, applyLog(c.result))
			w.settle()

			d := w.api.disruption(t, turnName)
			if d.Spec.Hold || d.Spec.KeepBootNext != c.keep {
				t.Errorf("%s: the Disruption's spec is %+v, want no hold and keepBootNext %v", c.name, d.Spec, c.keep)
			}
		})
	}
}
