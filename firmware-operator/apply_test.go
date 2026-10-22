package main

// These tests follow the apply of an approved update on a machine whose
// rehearsal passed: the request for a held reboot turn, the hold that a
// written update releases, the turn that a refused or failed write
// withdraws, the scan after the reboot that proves the write or fails
// it, and an operator that restarts in the middle. applysignal_test.go
// follows the apply pod from the request to the signal to write.

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/liken-sh/liken/kubernetes/conditions"
)

// turnName is the request for node-2's turn that the BIOS object files.
var turnName = disruptionName(biosObjectName, "node-2")

// preparingWorld is approvedWorld after node-2's rehearsal passed, so
// the apply pod runs on node-2 and prepares.
func preparingWorld(t *testing.T) *world {
	w := approvedWorld(t)
	w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded, rehearsalLog(passed))
	w.settle()
	w.api.runPod(t, w.onlyApply(t).Metadata.Name, false)
	w.settle()
	return w
}

// rehearsedWorld is preparingWorld after the apply pod prepared and
// turned Ready, so the operator has asked for node-2's turn.
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

// applies answers every stored apply pod.
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

// onlyApply answers the one apply pod, and fails the test when there
// is not exactly one.
func (w *world) onlyApply(t *testing.T) pod {
	t.Helper()
	applies := w.api.applies(t)
	if len(applies) != 1 {
		t.Fatalf("%d apply pods, want 1", len(applies))
	}
	return applies[0]
}

// applyLog is the log of an apply that answered.
func applyLog(r applyResult) string {
	line, _ := json.Marshal(r)
	return "fwupdtool: loading plugins\n" + string(line) + "\n"
}

// writtenResult is the answer of an apply whose append the firmware
// merged into dbx.
var writtenResult = applyResult{Result: resultWritten,
	Message: "wrote the 17260-byte append to dbx-d719b2cb-3d3a-4596-a3bc-dad00e67656f, which grew from 21352 to 21476 bytes"}

// onlyApplyRecord answers the BIOS object's one apply record, and fails
// the test when there is not exactly one.
func (w *world) onlyApplyRecord(t *testing.T) applyRecord {
	t.Helper()
	records := w.api.firmware(t, biosObjectName).Status.Applies
	if len(records) != 1 {
		t.Fatalf("status.applies = %+v, want one record", records)
	}
	return records[0]
}

// appliedOf answers the BIOS object's Applied condition, and whether it
// holds one.
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
			t.Errorf("the request is %+v, want %+v", d.Spec, want)
		}
		if d.Metadata.Labels[disruptionLabel] != biosObjectName || len(d.Metadata.OwnerReferences) != 1 || d.Metadata.OwnerReferences[0].UID != f.Metadata.UID {
			t.Errorf("the request's metadata is %+v, want the Firmware's label and the Firmware as its owner", d.Metadata)
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
// has no Applied condition until one machine asks for its turn.
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
					t.Error("the operator asked for node-2's turn")
				}
				if c, held := appliedOf(t, w); held {
					t.Errorf("Applied is %+v, want none", c)
				}
			})
		})
	}
}

// A rehearsal answers one boot, so a rehearsal that passed on the boot
// before a reboot asks for no turn.
func TestARehearsalOfAnEarlierBootRequestsNoTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := approvedWorld(t)
		name := w.onlyRehearsal(t).Metadata.Name
		w.api.putFleetMachine("node-2", machineReady, secondBoot)
		w.settle()
		w.api.finishPod(t, name, rehearsalContainer, podSucceeded, rehearsalLog(passed))
		w.settle()

		if w.api.disruptionExists(turnName) {
			t.Error("the operator asked for a turn on the rehearsal of the first boot")
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
			t.Errorf("the request is %+v, want no hold, so the turn reboots the machine", d.Spec)
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

// A write that the firmware refused, or an apply that reports it wrote
// nothing, needs no reboot, so the operator withdraws the turn, and
// writes nothing again on its own.
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
					t.Error("the request remains, want it withdrawn")
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

// An apply whose end does not say whether it wrote may have written, so
// the operator still releases the hold. The machine reboots in its
// turn, and the next scan answers whether the firmware took the append,
// instead of an unplanned boot later.
func TestAnUncertainWriteRebootsTheMachine(t *testing.T) {
	cases := []struct {
		name    string
		end     func(t *testing.T, w *world, name string)
		message string
	}{
		{"a crash", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podFailed, "fwupdtool: loading\npanic: runtime error\n")
		}, "the apply wrote no result; its last line was: panic: runtime error"},
		{"a deleted pod", func(t *testing.T, w *world, name string) {
			w.api.deleteObject(uploadPodsPath, name)
		}, "the apply pod is gone"},
		{"a pod that never finishes", func(t *testing.T, w *world, name string) {
			time.Sleep(scanDeadline)
		}, "the apply pod did not finish within 10m0s of the signal to write"},
		{"a crash after the prepare", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podFailed, preparedLine+"\n")
		}, "the apply wrote no result; its last line was: " + preparedLine},
		{"an answer the operator does not know", func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podSucceeded, applyLog(applyResult{Result: "Maybe", Message: "?"}))
		}, `the apply answered the result "Maybe", which the operator does not know`},
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
					t.Error("want the request kept with no hold, so the turn reboots the machine")
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

// A person who deletes the request while the apply pod runs ends the
// turn before the write. A write that landed, or may have, takes effect
// only at a reboot, so the operator asks for the machine's turn again,
// with no hold, while the machine still runs the boot of the write.
func TestADeletedRequestIsFiledAgainAfterAWrite(t *testing.T) {
	cases := []struct {
		name   string
		result string
		end    func(t *testing.T, w *world, name string)
	}{
		{"a written update", resultWritten, func(t *testing.T, w *world, name string) {
			w.api.finishPod(t, name, applyContainer, podSucceeded, applyLog(writtenResult))
		}},
		{"an uncertain write", resultUncertain, func(t *testing.T, w *world, name string) {
			w.api.deleteObject(uploadPodsPath, name)
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
					t.Fatal("no request, want the machine's turn asked for again")
				}
				if d := w.api.disruption(t, turnName); d.Spec.Hold || d.Spec.Kind != "Reboot" {
					t.Errorf("the request is %+v, want a Reboot with no hold", d.Spec)
				}
			})
		})
	}
}

// A machine that rebooted after the write needs no turn, even when its
// request is gone before the scan of the new boot.
func TestNoRequestIsFiledAgainAfterTheReboot(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rebootedWorld(t, rebootingEnds[0].end)
		w.api.deleteObject(disruptionsPath, turnName)
		w.settle()

		if w.api.disruptionExists(turnName) {
			t.Error("a request after the reboot, want none")
		}
	})
}

// A machine-operator that does not hold a turn reboots the machine and
// marks the request Complete, and a person can delete the request. The
// operator then deletes the apply pod that waits, writes nothing, and
// does not ask again on its own.
func TestATurnThatEndsWithoutAHoldFails(t *testing.T) {
	cases := []struct {
		name string
		end  func(t *testing.T, w *world)
		want string
	}{
		{"a turn with no hold", func(t *testing.T, w *world) { w.api.completeTurn(t, turnName) },
			"the machine left its turn before the hold took effect: the Disruption " + turnName + " is Complete"},
		{"a deleted request", func(t *testing.T, w *world) { w.api.deleteObject(disruptionsPath, turnName) },
			"the machine left its turn before the hold took effect: the Disruption " + turnName + " is gone"},
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
					t.Error("a request or an apply pod remains, want neither")
				}
			})
		})
	}
}

// rebootedWorld is heldWorld after the apply pod ended as end has it,
// and after machine-operator rebooted node-2 and marked the request
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

// The two ends of an apply that reboot the machine: the firmware took
// the append, or the operator cannot tell whether it did.
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
		w.api.deleteObject(uploadPodsPath, name)
	}, "the machine rebooted, and its next scan still reads version 0.0.3.5, not 0.0.4.2, so the append was not written"},
}

// While an apply waits for the scan of the new boot, the machine
// rehearses nothing, because the apply has its append.
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
				if _, held := conditions.Find(f.Status.Conditions, appliedCondition); held || len(f.Status.Applies) != 0 {
					t.Errorf("the status holds %+v and %v, want no apply once nothing is pending", f.Status.Applies, f.Status.Conditions)
				}
				if w.api.disruptionExists(turnName) {
					t.Error("the completed request remains, want it deleted")
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
					t.Error("the request remains, want it deleted and no second turn")
				}
			})
		})
	}
}

// An operator that stops after it sent the apply pod the signal, before
// the record said Writing, finds the turn from the Disruption, the
// attempt and the signal from the pod's annotations, and the record in
// the status. It follows the one pod to its result, and sends no second
// signal.
func TestARestartDuringTheWriteFollowsThePod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &world{api: startFakeAPI(t)}
		stopFirst := w.run(t)
		seedDiscovery(w)
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
			resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"), secureBootDBX("2"))))
		w.settle()
		approve(t, w, "0.0.4.2", biosCabinet, nil)
		w.api.finishPod(t, w.onlyRehearsal(t).Metadata.Name, rehearsalContainer, podSucceeded, rehearsalLog(passed))
		w.settle()
		w.api.readyPod(t, w.onlyApply(t).Metadata.Name)
		w.settle()
		// The API server refuses every status write until the first
		// operator stops, so its record stays Waiting after its pod.
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
			t.Error("the request still holds the turn")
		}
		if got := w.api.created(uploadPodsPath); got != 3 {
			t.Errorf("the operators created %d pods, want the scan, the rehearsal, and one apply", got)
		}
	})
}

// A withdrawn approval needs no turn, so the operator deletes a request
// that still holds one, and the apply pod that waits for it.
func TestWithdrawingTheApprovalWithdrawsTheTurn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := rehearsedWorld(t)
		f := w.api.firmware(t, biosObjectName)
		f.Spec.Update.Version, f.Spec.Update.Cabinet = "", nil
		w.api.putFirmware(f)
		w.settle()

		if w.api.disruptionExists(turnName) {
			t.Error("the request remains, want it withdrawn")
		}
		if records := w.api.firmware(t, biosObjectName).Status.Applies; len(records) != 0 {
			t.Errorf("status.applies = %+v, want none with no approval", records)
		}
		if got := len(w.api.applies(t)); got != 0 {
			t.Errorf("%d apply pods, want the waiting pod deleted", got)
		}
	})
}

// A request's name holds the machine and the Firmware for a person to
// read, and stays a DNS subdomain however long they are.
func TestTheRequestsNameIsADNSSubdomain(t *testing.T) {
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
