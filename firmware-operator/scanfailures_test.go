package main

// These tests cover the scans that do not answer: a catalog that moved
// under the scan, a scan that failed or crashed, a pod that never
// finishes or is deleted, and an operator that restarts during a scan.
// Each attempt runs once, so nothing retries a failed scan until the
// machine, its devices, or the catalog file change.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A scan that finds another file named in the jcat answers
// CatalogMoved, and the operator reads the jcat at once, not at the
// next hour, and scans against the new file.
func TestCatalogMovedReadsTheJcatAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		w.lvfs.publish("firmware-09139-stable.xml.zst", frameworkBIOS("0.0.3.5", "0.0.4.2", "0.0.4.3"))
		moved := scanResult{Result: resultCatalogMoved, Message: "the jcat names firmware-09139-stable.xml.zst", File: "firmware-09139-stable.xml.zst"}
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded, resultLog(moved))
		w.settle()

		if got := w.api.catalog(t, "lvfs").Status.File; got != "firmware-09139-stable.xml.zst" {
			t.Errorf("the status names %s, want the new file at once", got)
		}
		scan := w.onlyScan(t)
		if got := scanOf(scan).file; got != "firmware-09139-stable.xml.zst" {
			t.Errorf("the scan reads %s, want the new file", got)
		}
		want := []posted{{"Normal", reasonFetched}, {"Normal", reasonScanStarted}, {"Normal", reasonCatalogMoved}, {"Normal", reasonScanStarted}}
		if got := w.postedAboutCatalog("lvfs"); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}

// A failed scan does not run again for the same boot, devices, and
// file, even an hour later and after a replug.
func TestAFailedScanDoesNotRunAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		failed := scanResult{Result: resultFailed, Message: "fwupdtool refresh: checksum error", File: "firmware-09138-stable.xml.zst"}
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podFailed, resultLog(failed))
		w.settle()
		w.api.publish("node-2", camera("1-3", 5))
		time.Sleep(time.Hour)
		synctest.Wait()

		if got := w.api.created(uploadPodsPath); got != 1 {
			t.Errorf("the operator created %d scan pods, want 1", got)
		}
		r := w.record(t, "lvfs", "node-2")
		if r.Result != resultFailed || r.Message != failed.Message {
			t.Errorf("the record is %+v, want Failed with the scan's message", r)
		}
		want := []posted{{"Normal", reasonFetched}, {"Normal", reasonScanStarted}, {"Warning", reasonScanFailed}}
		if got := w.postedAboutCatalog("lvfs"); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}

// A scan that ends with no result line fails, and the record says why.
func TestAScanWithNoResultFails(t *testing.T) {
	cases := []struct {
		name  string
		phase string
		log   string
		want  string
	}{
		{"a crash", podFailed, "fwupdtool: loading\npanic: runtime error\n", "the scan wrote no result; its last line was: panic: runtime error"},
		{"no log", podFailed, "", "the scan pod wrote no log: the scan exited with code 1"},
		{"an unknown result", podSucceeded, `{"result":"Maybe"}` + "\n", `the scan answered the result "Maybe", which the operator does not know`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := discoveryWorld(t)
				w.api.finishScan(t, w.onlyScan(t).Metadata.Name, c.phase, c.log)
				w.settle()

				if r := w.record(t, "lvfs", "node-2"); r.Result != resultFailed || r.Message != c.want {
					t.Errorf("the record is %+v, want Failed with %q", r, c.want)
				}
				if got := len(w.api.scans(t)); got != 0 {
					t.Errorf("%d scan pods remain, want none", got)
				}
			})
		})
	}
}

// A scan pod that never finishes, such as one whose image never pulls,
// fails ten minutes after its creation.
func TestAScanThatDoesNotFinishFailsAfterTenMinutes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		time.Sleep(9 * time.Minute)
		synctest.Wait()
		if r := w.record(t, "lvfs", "node-2"); r.Result != resultRunning {
			t.Fatalf("before the deadline the record is %+v, want Running", r)
		}
		time.Sleep(time.Minute)
		synctest.Wait()

		r := w.record(t, "lvfs", "node-2")
		if r.Result != resultFailed || !strings.Contains(r.Message, "did not finish within 10m0s") {
			t.Errorf("the record is %+v, want Failed because the pod did not finish", r)
		}
		if got := len(w.api.scans(t)); got != 0 {
			t.Errorf("%d scan pods remain, want none", got)
		}
	})
}

// A Running record whose pod a person deleted fails, and the operator
// does not run the scan again for that attempt.
func TestARunningScanWhosePodIsGoneFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		w.api.deleteObject(uploadPodsPath, w.onlyScan(t).Metadata.Name)
		w.settle()

		r := w.record(t, "lvfs", "node-2")
		if r.Result != resultFailed || !strings.Contains(r.Message, "the scan pod is gone") {
			t.Errorf("the record is %+v, want Failed because the pod is gone", r)
		}
		if got := w.api.created(uploadPodsPath); got != 1 {
			t.Errorf("the operator created %d scan pods, want 1", got)
		}
	})
}

// An operator that restarts during a scan creates no second pod, and
// the new copy follows the first pod to its result.
func TestARestartDuringAScanCreatesNoSecondPod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &world{api: startFakeAPI(t)}
		stopFirst := w.run(t)
		w.lvfs.publish("firmware-09138-stable.xml.zst", frameworkBIOS("0.0.3.5", "0.0.4.2"))
		w.api.publish("node-2", camera("1-3", 4))
		w.settle()
		w.api.putFleetMachine("node-2", machineReady, firstBoot)
		w.api.putCatalog("lvfs", lvfsURL)
		w.settle()
		name := w.onlyScan(t).Metadata.Name
		stopFirst()
		w.run(t)
		w.api.finishScan(t, name, podSucceeded, resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"))))
		w.settle()

		if got := w.api.created(uploadPodsPath); got != 1 {
			t.Errorf("the operators created %d scan pods, want 1", got)
		}
		if r := w.record(t, "lvfs", "node-2"); r.Result != resultScanned || r.Releases == "" {
			t.Errorf("the record is %+v, want Scanned with a releases hash", r)
		}
	})
}

// A status write that the API server refuses keeps the scan pod, and
// the pass after the retry delay writes the result and then deletes the
// pod.
func TestARefusedCatalogWriteKeepsThePodUntilTheRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		w.api.refuseStatusWrites(1)
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
			resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"))))
		w.settle()

		if r := w.record(t, "lvfs", "node-2"); r.Result != resultRunning {
			t.Errorf("after the refusal the record is %+v, want Running", r)
		}
		if got := len(w.api.scans(t)); got != 1 {
			t.Errorf("after the refusal %d scan pods remain, want 1", got)
		}
		time.Sleep(retryDelay)
		synctest.Wait()

		if r := w.record(t, "lvfs", "node-2"); r.Result != resultScanned {
			t.Errorf("after the retry the record is %+v, want Scanned", r)
		}
		if got := len(w.api.scans(t)); got != 0 {
			t.Errorf("after the retry %d scan pods remain, want none", got)
		}
	})
}

// A restarted operator that cannot download the catalog file records
// the scan with no releases hash, so the next file scans the machine
// again rather than skip a release it could not compare.
func TestAFileThatCannotBeReadAgainScansAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &world{api: startFakeAPI(t)}
		stopFirst := w.run(t)
		w.lvfs.publish("firmware-09138-stable.xml.zst", frameworkBIOS("0.0.3.5", "0.0.4.2"))
		w.api.publish("node-2", camera("1-3", 4))
		w.settle()
		w.api.putFleetMachine("node-2", machineReady, firstBoot)
		w.api.putCatalog("lvfs", lvfsURL)
		w.settle()
		stopFirst()
		w.lvfs.serve(directoryOf(lvfsURL)+"firmware-09138-stable.xml.zst", []byte("gone"))
		w.run(t)
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
			resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"))))
		w.settle()
		if r := w.record(t, "lvfs", "node-2"); r.Result != resultScanned || r.Releases != "" {
			t.Fatalf("the record is %+v, want Scanned with no releases hash", r)
		}
		w.lvfs.publish("firmware-09139-stable.xml.zst", frameworkBIOS("0.0.3.5", "0.0.4.2"))
		time.Sleep(time.Hour)
		synctest.Wait()

		if got := w.api.created(uploadPodsPath); got != 2 {
			t.Errorf("the operators created %d scan pods, want a second for the new file", got)
		}
	})
}

// A scan whose log the operator cannot read stays Running while the
// operator tries again, and fails at the scan's deadline.
func TestAScanWhoseLogCannotBeReadFailsAtTheDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		w.api.mu.Lock()
		w.api.logsForbidden = true
		w.api.mu.Unlock()
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded, resultLog(scanned("firmware-09138-stable.xml.zst")))
		w.settle()
		if r := w.record(t, "lvfs", "node-2"); r.Result != resultRunning {
			t.Fatalf("before the deadline the record is %+v, want Running", r)
		}
		time.Sleep(scanDeadline)
		synctest.Wait()

		r := w.record(t, "lvfs", "node-2")
		if r.Result != resultFailed || !strings.Contains(r.Message, "could not read the scan's log") {
			t.Errorf("the record is %+v, want Failed because the log could not be read", r)
		}
	})
}
