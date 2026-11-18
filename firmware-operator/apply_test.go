package main

// These tests cover the apply of an approved update after a passed
// rehearsal, from the Disruption to the scan after the reboot.
// applysignal_test.go covers the apply pod from the Disruption to the
// signal to write.

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// turnName is the name of the Disruption that the operator creates for
// node-2's turn on the BIOS object.
var turnName = disruptionName(biosObjectName, "node-2")

// preparingWorld is approvedWorld after node-2's rehearsal passed, so
// the apply pod is running on node-2 and preparing.
func preparingWorld(t *testing.T) *world {
	w := approvedWorld(t)
	w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded, rehearsalLog(passed))
	w.settle()
	w.api.runPod(t, w.onlyApply(t).Metadata.Name, false)
	w.settle()
	return w
}

// rehearsedWorld is preparingWorld after the apply pod finished
// preparing and became Ready, so the operator has created the
// Disruption for node-2's turn.
func rehearsedWorld(t *testing.T) *world {
	w := preparingWorld(t)
	w.api.readyPod(t, w.onlyApply(t).Metadata.Name)
	w.settle()
	return w
}

// heldWorld is rehearsedWorld after machine-operator drained node-2 and
// held its turn, so the operator has sent the apply pod the signal to
// write.
func heldWorld(t *testing.T) *world {
	w := rehearsedWorld(t)
	w.api.holdTurn(t, turnName)
	w.settle()
	return w
}

func (a *fakeAPI) applies(t *testing.T) []pod {
	t.Helper()
	var out []pod
	for _, p := range a.uploads(t) {
		if p.Metadata.Labels[applyLabel] == applyLabelValue {
			out = append(out, p)
		}
	}
	return out
}

// onlyApply returns the apply pod, and fails the test unless there is
// exactly one.
func (w *world) onlyApply(t *testing.T) pod {
	t.Helper()
	applies := w.api.applies(t)
	if len(applies) != 1 {
		t.Fatalf("%d apply pods, want 1", len(applies))
	}
	return applies[0]
}

// applyLog returns the log of an apply pod that printed the result r.
func applyLog(r applyResult) string {
	line, _ := json.Marshal(r)
	return "fwupdtool: loading plugins\n" + string(line) + "\n"
}

// writtenResult is the result of an apply whose append the firmware
// merged into dbx.
var writtenResult = applyResult{Result: resultWritten,
	Message: "wrote the 17260-byte append to dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f, which grew from 21352 to 21476 bytes"}

// onlyApplyRecord returns the apply record of the BIOS object, and fails
// the test unless there is exactly one.
func (w *world) onlyApplyRecord(t *testing.T) applyRecord {
	t.Helper()
	records := w.api.firmware(t, biosObjectName).Status.Applies
	if len(records) != 1 {
		t.Fatalf("status.applies = %+v, want one record", records)
	}
	return records[0]
}

func appliedOf(t *testing.T, w *world) (condition, bool) {
	t.Helper()
	return conditions.Find(w.api.firmware(t, biosObjectName).Status.Conditions, appliedCondition)
}

func TestAPassedRehearsalRequestsAHeldTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsedWorld(t)

		f := w.api.firmware(t, biosObjectName)
		d := w.api.disruption(t, turnName)
		want := disruptionSpec{Machine: "node-2", Requester: requester, Kind: "Reboot", Hold: true,
			Reason: biosObjectName + ": install 0.0.4.2 of " + f.Spec.Update.Component}
		if d.Spec != want {
			t.Errorf("the Disruption's spec is %+v, want %+v", d.Spec, want)
		}
		if d.Metadata.Labels[disruptionLabel] != biosObjectName || len(d.Metadata.OwnerReferences) != 1 || d.Metadata.OwnerReferences[0].UID != f.Metadata.UID {
			t.Errorf("the Disruption's metadata is %+v, want the Firmware's label and the Firmware as its owner", d.Metadata)
		}
		r := w.onlyApplyRecord(t)
		if r.Result != resultWaiting || r.SHA256 != passed.SHA256 || r.Boot != firstBoot || r.Disruption != turnName {
			t.Errorf("the record is %+v, want Waiting for the rehearsed append on the first boot", r)
		}
		if c, _ := appliedOf(t, w); c.Status != conditions.Unknown || c.Reason != reasonApplying {
			t.Errorf("Applied is %+v, want Unknown with the reason Applying", c)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Normal", reasonApplyRequested}) {
			t.Errorf("the operator posted %v, want a Normal ApplyRequested", got)
		}
	})
}

// A machine whose rehearsal did not pass gets no turn, and the approval
// has no Applied condition while no machine has an apply record.
func TestARehearsalThatDidNotPassRequestsNoTurn(t *testing.T) {
	cases := []struct {
		name string
		end  func(t *testing.T, w *world)
	}{
		{"a rehearsal that runs", func(*testing.T, *world) {}},
		{"a refused rehearsal", func(t *testing.T, w *world) {
			w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded,
				rehearsalLog(rehearsalResult{Result: resultRefused, Message: "the install wrote EFI/fwupd/fwupdx64.efi under the ESP path"}))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := approvedWorld(t)
				c.end(t, w)
				w.settle()

				if w.api.disruptionExists(turnName) {
					t.Errorf("Disruption %s exists, want none", turnName)
				}
				if c, found := appliedOf(t, w); found {
					t.Errorf("Applied is %+v, want none", c)
				}
			})
		})
	}
}

// A rehearsal is valid only for the boot that it ran on, so a rehearsal
// that passed before a reboot does not request a turn.
func TestARehearsalOfAnEarlierBootRequestsNoTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := approvedWorld(t)
		name := w.onlyRehearsal(t).Metadata.Name
		w.api.putFleetMachine("node-2", machineReady, secondBoot)
		w.settle()
		w.api.finishPod(t, name, rehearsalContainer, podSucceeded, rehearsalLog(passed))
		w.settle()

		if w.api.disruptionExists(turnName) {
			t.Errorf("Disruption %s exists, want none because the rehearsal ran on the first boot", turnName)
		}
	})
}

func TestAWrittenUpdateReleasesTheHold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := heldWorld(t)
		w.api.finishPod(t, w.onlyApply(t).Metadata.Name, applyContainer, podSucceeded, applyLog(writtenResult))
		w.settle()

		if r := w.onlyApplyRecord(t); r.Result != resultWritten || r.Message != writtenResult.Message {
			t.Errorf("the record is %+v, want Written with the pod's message", r)
		}
		if d := w.api.disruption(t, turnName); d.Spec.Hold {
			t.Errorf("the Disruption's spec is %+v, want no hold, so the turn reboots the machine", d.Spec)
		}
		if got := len(w.api.applies(t)); got != 0 {
			t.Errorf("%d apply pods remain, want none", got)
		}
		if c, _ := appliedOf(t, w); c.Status != conditions.True || c.Reason != resultWritten {
			t.Errorf("Applied is %+v, want True with the reason Written", c)
		}
		if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Normal", reasonApplyWritten}) {
			t.Errorf("the operator posted %v, want a Normal ApplyWritten", got)
		}
	})
}

// A write that the firmware refused, or an apply that reports that it
// wrote nothing, needs no reboot. The operator deletes the Disruption
// and does not try the write again on its own.
func TestARefusedOrFailedWriteWithdrawsTheTurn(t *testing.T) {
	cases := []struct {
		name          string
		end           func(t *testing.T, w *world, name string)
		result, event string
		message       string
	}{
		{"a refusal", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podSucceeded, applyLog(applyResult{Result: resultRefused,
				Message: "the firmware refused the append to dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f: permission denied"}))
		}, resultRefused, reasonApplyRefused, "the firmware refused the append"},
		{"changed variables", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podSucceeded, applyLog(applyResult{Result: resultFailed,
				Message: "the append's SHA-256 is now 0b1c, and the rehearsal recorded cf901743: the machine's variables changed since the rehearsal, so nothing was written"}))
		}, resultFailed, reasonApplyFailed, "the append's SHA-256 is now 0b1c"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := heldWorld(t)
				c.end(t, w, w.onlyApply(t).Metadata.Name)
				w.settle()
				time.Sleep(time.Hour)
				synctest.Wait()

				if r := w.onlyApplyRecord(t); r.Result != c.result || !strings.HasPrefix(r.Message, c.message) {
					t.Errorf("the record is %+v, want %s with %q", r, c.result, c.message)
				}
				if w.api.disruptionExists(turnName) {
					t.Error("the Disruption remains, want it deleted")
				}
				if ac, _ := appliedOf(t, w); ac.Status != conditions.False || ac.Reason != c.result {
					t.Errorf("Applied is %+v, want False with the reason %s", ac, c.result)
				}
				if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Warning", c.event}) {
					t.Errorf("the operator posted %v, want a Warning %s", got, c.event)
				}
				if got := len(w.api.applies(t)); got != 0 {
					t.Errorf("%d apply pods, want none started again", got)
				}
			})
		})
	}
}

// An apply that ends without saying whether it wrote may have written,
// so the operator still removes the hold. The machine reboots during its
// turn, and the next scan shows whether the firmware took the append,
// instead of an unplanned reboot later.
func TestAnUncertainWriteRebootsTheMachine(t *testing.T) {
	cases := []struct {
		name    string
		end     func(t *testing.T, w *world, name string)
		message string
	}{
		{"a crash", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podFailed, "fwupdtool: loading\npanic: runtime error\n")
		}, "the apply pod wrote no result, and its last log line is: panic: runtime error"},
		{"a deleted pod", func(t *testing.T, w *world, name string) {
			w.api.deleteObject(operatorPodsPath, name)
		}, "the apply pod no longer exists"},
		{"a pod that never finishes", func(t *testing.T, w *world, name string) {
			time.Sleep(scanDeadline)
		}, "the apply pod did not finish within 10m0s of the signal to write"},
		{"a crash after the prepare", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podFailed, preparedLine+"\n")
		}, "the apply pod wrote no result, and its last log line is: " + preparedLine},
		{"an answer the operator does not know", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podSucceeded, applyLog(applyResult{Result: "Maybe", Message: "?"}))
		}, `the apply pod reported the result "Maybe", which the operator does not recognize`},
		{"a firmware that accepted only some of the writes", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podSucceeded, applyLog(applyResult{Result: resultUncertain,
				Message: "the firmware accepted 1 of the 2 authenticated writes to db, and write 2 failed: permission denied"}))
		}, "the firmware accepted 1 of the 2 authenticated writes to db"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := heldWorld(t)
				c.end(t, w, w.onlyApply(t).Metadata.Name)
				w.settle()
				time.Sleep(time.Hour)
				synctest.Wait()

				if r := w.onlyApplyRecord(t); r.Result != resultUncertain || !strings.HasPrefix(r.Message, c.message) {
					t.Errorf("the record is %+v, want Uncertain with %q", r, c.message)
				}
				if !w.api.disruptionExists(turnName) || w.api.disruption(t, turnName).Spec.Hold {
					t.Error("the Disruption was deleted or still has spec.hold, want it kept with no hold so the turn reboots the machine")
				}
				if ac, _ := appliedOf(t, w); ac.Status != conditions.Unknown || ac.Reason != resultUncertain {
					t.Errorf("Applied is %+v, want Unknown with the reason Uncertain", ac)
				}
				if got := w.postedAbout(biosObjectName); !containsPosted(got, posted{"Warning", reasonApplyUncertain}) {
					t.Errorf("the operator posted %v, want a Warning %s", got, reasonApplyUncertain)
				}
				if got := len(w.api.applies(t)); got != 0 {
					t.Errorf("%d apply pods, want none started again", got)
				}
			})
		})
	}
}

// A person who deletes the Disruption while the apply pod runs ends the
// turn before the reboot. A write that happened, or may have, takes
// effect only at a reboot, so the operator creates the Disruption again,
// with no hold, while the machine still runs the boot of the write.
func TestADeletedDisruptionIsCreatedAgainAfterAWrite(t *testing.T) {
	cases := []struct {
		name   string
		result string
		end    func(t *testing.T, w *world, name string)
	}{
		{"a written update", resultWritten, func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podSucceeded, applyLog(writtenResult))
		}},
		{"an uncertain write", resultUncertain, func(t *testing.T, w *world, name string) {
			w.api.deleteObject(operatorPodsPath, name)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := heldWorld(t)
				pod := w.onlyApply(t).Metadata.Name
				w.api.deleteObject(disruptionsPath, turnName)
				w.settle()
				c.end(t, w, pod)
				w.settle()

				if r := w.onlyApplyRecord(t); r.Result != c.result {
					t.Errorf("the record is %+v, want %s", r, c.result)
				}
				if !w.api.disruptionExists(turnName) {
					t.Fatal("no Disruption exists, want one created again for the machine's turn")
				}
				if d := w.api.disruption(t, turnName); d.Spec.Hold || d.Spec.Kind != "Reboot" {
					t.Errorf("the Disruption's spec is %+v, want a Reboot with no hold", d.Spec)
				}
			})
		})
	}
}

// A machine that rebooted after the write needs no turn, even when its
// Disruption is gone before the scan of the new boot.
func TestNoDisruptionIsCreatedAgainAfterTheReboot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rebootedWorld(t, rebootingEnds[0].end)
		w.api.deleteObject(disruptionsPath, turnName)
		w.settle()

		if w.api.disruptionExists(turnName) {
			t.Error("a Disruption exists after the reboot, want none")
		}
	})
}

// A machine-operator that does not support spec.hold reboots the machine
// and marks the Disruption Complete, and a person can delete the
// Disruption. The operator then deletes the waiting apply pod, writes
// nothing, and does not request a turn again on its own.
func TestATurnThatEndsWithoutAHoldFails(t *testing.T) {
	cases := []struct {
		name string
		end  func(t *testing.T, w *world)
		want string
	}{
		{"a turn with no hold", func(t *testing.T, w *world) { w.api.completeTurn(t, turnName) },
			"the Disruption " + turnName + " is Complete, so the machine left its reboot turn"},
		{"a deleted Disruption", func(t *testing.T, w *world) { w.api.deleteObject(disruptionsPath, turnName) },
			"the Disruption " + turnName + " no longer exists, so the machine left its reboot turn"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := rehearsedWorld(t)
				c.end(t, w)
				w.settle()

				if r := w.onlyApplyRecord(t); r.Result != resultFailed || !strings.HasPrefix(r.Message, c.want) {
					t.Errorf("the record is %+v, want Failed with %q", r, c.want)
				}
				if w.api.disruptionExists(turnName) || len(w.api.applies(t)) != 0 {
					t.Error("a Disruption or an apply pod remains, want neither")
				}
			})
		})
	}
}

// rebootedWorld is heldWorld after the apply pod ended as end specifies,
// and after machine-operator rebooted node-2 and marked the Disruption
// Complete. The scan of the new boot has started.
func rebootedWorld(t *testing.T, end func(t *testing.T, w *world, name string)) *world {
	w := heldWorld(t)
	end(t, w, w.onlyApply(t).Metadata.Name)
	w.settle()
	w.api.putFleetMachine("node-2", machineReady, secondBoot)
	w.api.completeTurn(t, turnName)
	w.settle()
	return w
}

// The ends of an apply after which the machine reboots: the firmware
// took the append, or the operator cannot tell whether it did.
var rebootingEnds = []struct {
	name   string
	result string
	end    func(t *testing.T, w *world, name string)
	failed string
}{
	{"a written update", resultWritten, func(t *testing.T, w *world, name string) {
		w.api.finishPod(t, name, applyContainer, podSucceeded, applyLog(writtenResult))
	}, "the machine rebooted after the write, and its next scan still reads version 0.0.3.5, not 0.0.4.2"},
	{"an uncertain write", resultUncertain, func(t *testing.T, w *world, name string) {
		w.api.deleteObject(operatorPodsPath, name)
	}, "the machine rebooted, and its next scan still reads version 0.0.3.5, not 0.0.4.2, so the append was not written"},
}

// While an apply waits for the scan of the new boot, the operator
// starts no rehearsal on the machine, because the apply already has its
// append.
func TestNoRehearsalStartsWhileAWriteWaitsForItsReboot(t *testing.T) {
	for _, c := range rebootingEnds {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := rebootedWorld(t, c.end)

				if got := len(w.api.rehearsals(t)); got != 0 {
					t.Errorf("%d rehearsal pods after the reboot, want none", got)
				}
				if r := w.onlyApplyRecord(t); r.Result != c.result {
					t.Errorf("the record is %+v, want %s until the scan", r, c.result)
				}
			})
		})
	}
}

func TestTheNextScanProvesTheWrite(t *testing.T) {
	for _, c := range rebootingEnds {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := rebootedWorld(t, c.end)
				w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
					resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.4.2"), secureBootDBX("2"))))
				w.settle()

				f := w.api.firmware(t, biosObjectName)
				if c, _ := conditions.Find(f.Status.Conditions, approvedCondition); c.Reason != reasonInstalled {
					t.Errorf("Approved is %+v, want the reason Installed", c)
				}
				if _, found := conditions.Find(f.Status.Conditions, appliedCondition); found || len(f.Status.Applies) != 0 {
					t.Errorf("the status has %+v and %v, want no apply once nothing is pending", f.Status.Applies, f.Status.Conditions)
				}
				if w.api.disruptionExists(turnName) {
					t.Error("the Complete Disruption remains, want it deleted")
				}
			})
		})
	}
}

func TestAMachineThatStillReadsTheOldVersionAfterItsRebootFails(t *testing.T) {
	for _, c := range rebootingEnds {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := rebootedWorld(t, c.end)
				w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
					resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"), secureBootDBX("2"))))
				w.settle()

				if r := w.onlyApplyRecord(t); r.Result != resultFailed || r.Message != c.failed {
					t.Errorf("the record is %+v, want Failed: %s", r, c.failed)
				}
				if w.api.disruptionExists(turnName) {
					t.Error("the Disruption remains, want it deleted and no second turn")
				}
			})
		})
	}
}

// An operator that stops after it sends the apply pod the signal, and
// before it records Writing, loses nothing. A new operator reads the turn
// from the Disruption, the attempt and the signal from the pod's
// annotations, and the record from status. It follows the same pod to
// its result and does not send a second signal.
func TestARestartDuringTheWriteFollowsThePod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &world{api: startFakeAPI(t)}
		stopFirst := w.run(t)
		setUpDiscovery(w)
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
			resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"), secureBootDBX("2"))))
		w.settle()
		approve(t, w, "0.0.4.2", biosCabinet, nil)
		w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded, rehearsalLog(passed))
		w.settle()
		w.api.readyPod(t, w.onlyApply(t).Metadata.Name)
		w.settle()
		// The fake API server refuses every status write until the first
		// operator stops, so the record stays Waiting after the pod
		// receives the signal.
		w.api.refuseStatusWrites(100)
		w.api.holdTurn(t, turnName)
		w.settle()
		stopFirst()
		w.api.refuseStatusWrites(0)
		if r := w.onlyApplyRecord(t); r.Result != resultWaiting {
			t.Fatalf("before the restart the record is %+v, want Waiting", r)
		}
		sent := w.onlyApply(t).Metadata.Annotations[writeAtKey]
		time.Sleep(time.Minute)
		w.run(t)
		if r, p := w.onlyApplyRecord(t), w.onlyApply(t); r.Result != resultWriting || p.Metadata.Annotations[writeAtKey] != sent {
			t.Errorf("after the restart the record is %+v and the signal was sent at %s, want Writing and the first signal at %s",
				r, p.Metadata.Annotations[writeAtKey], sent)
		}
		w.api.finishPod(t, w.onlyApply(t).Metadata.Name, applyContainer, podSucceeded, applyLog(writtenResult))
		w.settle()

		if r := w.onlyApplyRecord(t); r.Result != resultWritten {
			t.Errorf("the record is %+v, want Written", r)
		}
		if d := w.api.disruption(t, turnName); d.Spec.Hold {
			t.Error("the Disruption still has spec.hold, want it removed")
		}
		if got := w.api.created(operatorPodsPath); got != 3 {
			t.Errorf("the operator created %d pods, want the scan, the rehearsal, and one apply", got)
		}
	})
}

// A withdrawn approval needs no turn, so the operator deletes a
// Disruption that still holds one, and the apply pod that waits for it.
func TestWithdrawingTheApprovalWithdrawsTheTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsedWorld(t)
		f := w.api.firmware(t, biosObjectName)
		f.Spec.Update.Version, f.Spec.Update.Cabinet = "", nil
		w.api.putFirmware(f)
		w.settle()

		if w.api.disruptionExists(turnName) {
			t.Error("the Disruption remains, want it deleted")
		}
		if records := w.api.firmware(t, biosObjectName).Status.Applies; len(records) != 0 {
			t.Errorf("status.applies = %+v, want none with no approval", records)
		}
		if got := len(w.api.applies(t)); got != 0 {
			t.Errorf("%d apply pods, want the waiting pod deleted", got)
		}
	})
}

// A Disruption's name contains the machine and the Firmware so that a
// person can read it, and it stays a valid DNS subdomain however long
// they are.
func TestTheDisruptionNameIsADNSSubdomain(t *testing.T) {
	cases := []struct {
		name, firmware, machine, want string
	}{
		{"short names", "microsoft-dbx-x64", "node-2", "node-2.firmware-microsoft-dbx-x64"},
		{"long names", strings.Repeat("f", 250), "node-2", "node-2.firmware-" + strings.Repeat("f", 237)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := disruptionName(c.firmware, c.machine); got != c.want {
				t.Errorf("disruptionName = %q, want %q", got, c.want)
			}
		})
	}
}
