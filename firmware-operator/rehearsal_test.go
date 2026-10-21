package main

// These tests follow the rehearsal of an approved release on each
// machine the approval waits for: the pod that starts, the record and
// the Rehearsed condition that each result writes, the attempt that
// runs once, and the records that leave with the approval.

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// rehearsals answers every stored rehearsal pod.
func (a *fakeAPI) rehearsals(t *testing.T) []pod {
	t.Helper()
	var out []pod
	for _, p := range a.uploads(t) {
		if p.Metadata.Labels[rehearsalLabel] == rehearsalLabelValue {
			out = append(out, p)
		}
	}
	return out
}

// onlyRehearsal answers the one rehearsal pod, and fails the test when
// there is not exactly one.
func (w *world) onlyRehearsal(t *testing.T) pod {
	t.Helper()
	rehearsals := w.api.rehearsals(t)
	if len(rehearsals) != 1 {
		t.Fatalf("%d rehearsal pods, want 1", len(rehearsals))
	}
	return rehearsals[0]
}

// rehearsalLog is the log of a rehearsal that answered.
func rehearsalLog(r rehearsalResult) string {
	line, _ := json.Marshal(r)
	return "fwupdtool: loading plugins\n" + string(line) + "\n"
}

// passed is the result of a rehearsal whose install appended 17,260
// bytes to dbx.
var passed = rehearsalResult{Result: resultPassed, Message: "the install appends 17260 bytes to dbx",
	Variable: "dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f", Bytes: 17260, SHA256: "cf901743", Free: 65536}

// rehearsedOf answers the BIOS object's Rehearsed condition, and
// whether it holds one.
func rehearsedOf(t *testing.T, w *world) (condition, bool) {
	t.Helper()
	return conditions.Find(w.api.firmware(t, biosObjectName).Status.Conditions, rehearsedCondition)
}

// approvedWorld is scannedWorld after a person approved BIOS 0.0.4.2,
// which node-2 waits for, so its rehearsal has started.
func approvedWorld(t *testing.T) *world {
	w := scannedWorld(t)
	approve(t, w, "0.0.4.2", biosCabinet, nil)
	return w
}

func TestAnApprovalRehearsesOnEachPendingMachine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := approvedWorld(t)

		p := w.onlyRehearsal(t)
		if p.Spec.NodeName != "node-2" || !slices.Equal(p.Spec.Containers[0].Args, []string{"rehearse"}) {
			t.Errorf("the pod runs %v on %q, want rehearse on node-2", p.Spec.Containers[0].Args, p.Spec.NodeName)
		}
		env := map[string]string{}
		for _, e := range p.Spec.Containers[0].Env {
			env[e.Name] = e.Value
		}
		if env[deviceIDVariable] != "3fcf" || env[cabinetSHA256Variable] != biosCabinet || env[catalogFileVariable] != "firmware-09138-stable.xml.zst" {
			t.Errorf("the environment is %v, want the device, the pinned cabinet, and the scan's file", env)
		}
		c, _ := rehearsedOf(t, w)
		if c.Status != conditions.Unknown || c.Reason != reasonRehearsing {
			t.Errorf("Rehearsed is %+v, want Unknown with the reason Rehearsing", c)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Normal", reasonRehearsalStarted}) {
			t.Errorf("the operator posted %v, want a Normal RehearsalStarted", got)
		}
	})
}

func TestARehearsalPodCannotWriteTheMachine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := approvedWorld(t)

		p := w.onlyRehearsal(t)
		security := p.Spec.Containers[0].SecurityContext
		if security.Privileged != nil || !slices.Equal(security.Capabilities.Add, []string{"DAC_READ_SEARCH"}) {
			t.Errorf("the security context is %+v, want no privilege and DAC_READ_SEARCH alone", security)
		}
		mount := p.Spec.Containers[0].VolumeMounts[0]
		if mount.MountPath != hostFirmwarePath || !mount.ReadOnly || mount.RecursiveReadOnly != "Enabled" {
			t.Errorf("the firmware mount is %+v, want it read-only all the way down", mount)
		}
	})
}

func TestAPassedRehearsalRecordsTheAppend(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := approvedWorld(t)
		w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded, rehearsalLog(passed))
		w.settle()

		records := w.api.firmware(t, biosObjectName).Status.Rehearsals
		want := variableEdit{Variable: passed.Variable, Bytes: 17260, SHA256: "cf901743"}
		if len(records) != 1 || records[0].Result != resultPassed || *records[0].Append != want || records[0].Free != 65536 {
			t.Errorf("the rehearsals are %+v, want node-2 Passed with the append", records)
		}
		if c, _ := rehearsedOf(t, w); c.Status != conditions.True || c.Reason != resultPassed {
			t.Errorf("Rehearsed is %+v, want True with the reason Passed", c)
		}
		if got := len(w.api.rehearsals(t)); got != 0 {
			t.Errorf("%d rehearsal pods remain, want none", got)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Normal", reasonRehearsalPassed}) {
			t.Errorf("the operator posted %v, want a Normal RehearsalPassed", got)
		}
	})
}

func TestARefusedRehearsalIsNotRehearsed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := approvedWorld(t)
		refused := rehearsalResult{Result: resultRefused, Message: "the install created BootNext-8be4df61-93ca-11d2-aa0d-00e098032b8c"}
		w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded, rehearsalLog(refused))
		w.settle()

		c, _ := rehearsedOf(t, w)
		if c.Status != conditions.False || c.Reason != resultRefused || c.Message != "node-2: "+refused.Message {
			t.Errorf("Rehearsed is %+v, want False with the reason Refused and node-2's message", c)
		}
		got := w.postedAbout(biosObjectName)
		if !containsPosted(got, posted{"Warning", reasonRehearsalRefused}) || !containsPosted(got, posted{"Warning", resultRefused}) {
			t.Errorf("the operator posted %v, want a Warning RehearsalRefused and the Warning transition", got)
		}
	})
}

func TestARehearsalRunsOncePerBoot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := approvedWorld(t)
		w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podFailed,
			rehearsalLog(rehearsalResult{Result: resultFailed, Message: "downloading the cabinet: timeout"}))
		w.settle()
		time.Sleep(time.Hour)
		synctest.Wait()
		if got := w.api.created(uploadPodsPath); got != 2 {
			t.Fatalf("before the reboot the operator created %d pods, want the scan and one rehearsal", got)
		}

		w.api.putFleetMachine("node-2", machineReady, secondBoot)
		w.settle()

		if got := len(w.api.rehearsals(t)); got != 1 {
			t.Errorf("after the reboot %d rehearsal pods run, want 1", got)
		}
	})
}

func TestARehearsalWaitsForAReadyMachine(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		w.api.putFleetMachine("node-2", "NotReady", firstBoot)
		w.settle()
		approve(t, w, "0.0.4.2", biosCabinet, nil)

		if got := len(w.api.rehearsals(t)); got != 0 {
			t.Errorf("%d rehearsal pods, want none on a machine that is not Ready", got)
		}
		if c, _ := rehearsedOf(t, w); c.Status != conditions.Unknown || !strings.Contains(c.Message, "node-2") {
			t.Errorf("Rehearsed is %+v, want Unknown and waiting for node-2", c)
		}
	})
}

func TestWithdrawingTheApprovalRemovesTheRehearsals(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := approvedWorld(t)
		w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded, rehearsalLog(passed))
		w.settle()
		f := w.api.firmware(t, biosObjectName)
		f.Spec.Update.Version, f.Spec.Update.Cabinet = "", nil
		w.api.putFirmware(f)
		w.settle()

		f = w.api.firmware(t, biosObjectName)
		if _, held := conditions.Find(f.Status.Conditions, rehearsedCondition); held || len(f.Status.Rehearsals) != 0 {
			t.Errorf("the status holds %+v and %v, want no rehearsal with no approval", f.Status.Rehearsals, f.Status.Conditions)
		}
	})
}

func TestARehearsalThatEndsBadlyFails(t *testing.T) {
	cases := []struct {
		name string
		end  func(t *testing.T, w *world, name string)
		want string
	}{
		{"a crash", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, rehearsalContainer, podFailed, "fwupdtool: loading\npanic: runtime error\n")
		}, "the rehearsal wrote no result; its last line was: panic: runtime error"},
		{"no log", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, rehearsalContainer, podFailed, "")
		}, "the rehearsal pod wrote no log: the rehearsal exited with code 1"},
		{"a deleted pod", func(t *testing.T, w *world, name string) {
			w.api.deleteObject(uploadPodsPath, name)
		}, "the rehearsal pod is gone"},
		{"a pod that never finishes", func(t *testing.T, w *world, name string) {
			time.Sleep(scanDeadline)
		}, "the rehearsal pod did not finish within 10m0s of its creation"},
		{"an unknown result", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, rehearsalContainer, podSucceeded, `{"result":"Maybe"}`+"\n")
		}, `the rehearsal answered the result "Maybe", which the operator does not know`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := approvedWorld(t)
				c.end(t, w, w.onlyRehearsal(t).Metadata.Name)
				w.settle()

				records := w.api.firmware(t, biosObjectName).Status.Rehearsals
				if len(records) != 1 || records[0].Result != resultFailed || !strings.HasPrefix(records[0].Message, c.want) {
					t.Errorf("the rehearsals are %+v, want Failed with %q", records, c.want)
				}
				if rc, _ := rehearsedOf(t, w); rc.Status != conditions.False || rc.Reason != resultFailed {
					t.Errorf("Rehearsed is %+v, want False with the reason Failed", rc)
				}
			})
		})
	}
}

// A rehearsal pod's name holds the Firmware and the machine for a
// person to read, and stays a DNS label however long they are.
func TestTheRehearsalPodsNameIsADNSLabel(t *testing.T) {
	cases := []struct {
		name, firmware, machine, wantPrefix string
	}{
		{"short names", "microsoft-dbx-x64", "node-2", "microsoft-dbx-x64-node-2-rehearsal-"},
		{"a long machine name", "microsoft-dbx-x64", strings.Repeat("m", 60), "microsoft-dbx-x64-" + strings.Repeat("m", 22) + "-rehearsal-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rehearsalPodName(rehearsalAttempt{firmware: c.firmware, machine: c.machine, boot: firstBoot, version: "20260707"})
			if len(got) > 63 || !strings.HasPrefix(got, c.wantPrefix) || strings.Contains(got, "--") {
				t.Errorf("rehearsalPodName = %q, want at most 63 characters that start with %q", got, c.wantPrefix)
			}
		})
	}
}

// An entry that names no fwupd device ID gives a rehearsal no device to
// install on, so the machine waits for a scan that records one.
func TestAnEntryWithNoDeviceIDStartsNoRehearsal(t *testing.T) {
	entries := []machineEntry{
		{Machine: "node-3", DeviceID: "3fcf", Releases: systemFirmware("0.0.3.5", "0.0.4.2").Releases},
		{Machine: "node-2", Releases: systemFirmware("0.0.3.5", "0.0.4.2").Releases},
	}
	approved := &update{Version: "0.0.4.2", Cabinet: &pinnedCabinet{SHA256: biosCabinet}}

	if e, offered := offeredEntry(entries, "node-2", approved); offered {
		t.Errorf("offeredEntry answered %+v, want none for an entry with no device ID", e)
	}
}
