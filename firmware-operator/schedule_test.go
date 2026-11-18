package main

// A machine is scanned again after a reboot, when a device name
// changes, or when a new catalog file offers different releases for its
// device GUIDs. These tests make each change after a scan and count the
// scan pods that the operator creates.

import (
	"testing"
	"testing/synctest"
	"time"
)

// A new boot time or a new device name starts a scan. A new device
// number, after a replug, does not.
func TestWhatStartsAnotherScan(t *testing.T) {
	cases := []struct {
		name   string
		change func(w *world)
		want   int
	}{
		{"a reboot", func(w *world) { w.api.putFleetMachine("node-2", machineReady, secondBoot) }, 2},
		{"a new device", func(w *world) { w.api.publish("node-2", camera("1-3", 4), camera("1-5", 7)) }, 2},
		{"a replug", func(w *world) { w.api.publish("node-2", camera("1-3", 5)) }, 1},
		{"a machine that is not Ready", func(w *world) { w.api.putFleetMachine("node-2", "Updating", secondBoot) }, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				w := scannedWorld(t)
				c.change(w)
				w.settle()

				if got := w.api.created(operatorPodsPath); got != c.want {
					t.Errorf("the operator created %d scan pods, want %d", got, c.want)
				}
			})
		})
	}
}

func TestAMachineIsScannedWhenItBecomesReady(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := startWorld(t)
		w.lvfs.publish("firmware-09138-stable.xml.zst", frameworkBIOS("0.0.4.2"))
		w.api.putFleetMachine("node-2", "Booting", firstBoot)
		w.api.putCatalog("lvfs", lvfsURL)
		w.settle()
		if got := len(w.api.scans(t)); got != 0 {
			t.Fatalf("%d scan pods while the machine boots, want none", got)
		}
		w.api.putFleetMachine("node-2", machineReady, firstBoot)
		w.settle()

		if got := len(w.api.scans(t)); got != 1 {
			t.Errorf("%d scan pods once the machine is Ready, want 1", got)
		}
	})
}

// fleetWorld is two machines scanned against the first catalog file.
// node-2 has the BIOS and the dbx, and node-3 has only the dbx.
func fleetWorld(t *testing.T) *world {
	w := discoveryWorld(t)
	w.api.publish("node-3", camera("1-2", 3))
	w.settle()
	w.api.putFleetMachine("node-3", machineReady, firstBoot)
	w.settle()
	for _, scan := range w.api.scans(t) {
		result := scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"), secureBootDBX("2"))
		if scanOf(scan).machine == "node-3" {
			result = scanned("firmware-09138-stable.xml.zst", secureBootDBX("2"))
		}
		w.api.finishScan(t, scan.Metadata.Name, podSucceeded, resultLog(result))
	}
	w.settle()
	return w
}

// A new catalog file that changes no release for a machine's GUIDs
// updates the machine's record to the new file without a scan. A file
// that changes the BIOS's releases scans only the machine that has the
// BIOS.
func TestANewFileScansOnlyTheMachinesItChanges(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := fleetWorld(t)
		w.lvfs.publish("firmware-09139-stable.xml.zst", frameworkBIOS("0.0.3.5", "0.0.4.2", "0.0.4.3"), dbx("1", "2"))
		time.Sleep(time.Hour)
		synctest.Wait()

		scans := w.api.scans(t)
		if len(scans) != 1 || scanOf(scans[0]).machine != "node-2" || scanOf(scans[0]).file != "firmware-09139-stable.xml.zst" {
			t.Fatalf("the scan pods are %+v, want one of node-2 against the new file", scans)
		}
		r := w.record(t, "lvfs", "node-3")
		if r.Result != resultScanned || r.File != "firmware-09139-stable.xml.zst" {
			t.Errorf("node-3's record is %+v, want Scanned against the new file", r)
		}
	})
}

// A new catalog file that changes no machine's releases scans no
// machine.
func TestANewFileWithTheSameReleasesScansNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := fleetWorld(t)
		w.lvfs.publish("firmware-09139-stable.xml.zst", frameworkBIOS("0.0.3.5", "0.0.4.2"), dbx("1", "2"),
			fwComponent{id: "com.other", guids: []string{meGUID}, versions: []string{"9"}})
		time.Sleep(time.Hour)
		synctest.Wait()

		if got := w.api.created(operatorPodsPath); got != 2 {
			t.Errorf("the operator created %d scan pods, want only the first two", got)
		}
		for _, machine := range []string{"node-2", "node-3"} {
			if r := w.record(t, "lvfs", machine); r.File != "firmware-09139-stable.xml.zst" {
				t.Errorf("%s's record is %+v, want the new file", machine, r)
			}
		}
	})
}

// A new scan image can find devices and releases that the old one did
// not, so an operator that starts with a different scan image scans
// each machine again, even with no reboot and no new catalog file.
func TestAnOperatorWithANewScanImageScansAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &world{api: startFakeAPI(t)}
		stopFirst := w.run(t)
		w.lvfs.publish("firmware-09138-stable.xml.zst", frameworkBIOS("0.0.3.5", "0.0.4.2"))
		w.api.publish("node-2", camera("1-3", 4))
		w.settle()
		w.api.putFleetMachine("node-2", machineReady, firstBoot)
		w.settle()
		w.api.putCatalog("lvfs", lvfsURL)
		w.settle()
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
			resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"))))
		w.settle()
		stopFirst()
		w.scanImage = "ghcr.io/liken-sh/firmware-operator-scan:next"
		w.run(t)

		if got := w.api.created(operatorPodsPath); got != 2 {
			t.Fatalf("the operators created %d scan pods, want 2", got)
		}
		if image := w.onlyScan(t).Spec.Containers[0].Image; image != w.scanImage {
			t.Errorf("the second scan runs %s, want %s", image, w.scanImage)
		}
		if r := w.record(t, "lvfs", "node-2"); r.Image != w.scanImage || r.Result != resultRunning {
			t.Errorf("the record is %+v, want a Running scan with the new image", r)
		}
	})
}

// A restarted operator has no cached copy of the old catalog file. It
// downloads the file again to compare releases, and still scans only
// the machines whose releases the new file changes.
func TestARestartedOperatorComparesReleasesAgain(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := &world{api: startFakeAPI(t)}
		stopFirst := w.run(t)
		w.lvfs.publish("firmware-09138-stable.xml.zst", frameworkBIOS("0.0.3.5", "0.0.4.2"))
		w.api.publish("node-2", camera("1-3", 4))
		w.settle()
		w.api.putFleetMachine("node-2", machineReady, firstBoot)
		w.settle()
		w.api.putCatalog("lvfs", lvfsURL)
		w.settle()
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
			resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"))))
		w.settle()
		stopFirst()
		w.lvfs.publish("firmware-09139-stable.xml.zst", frameworkBIOS("0.0.3.5", "0.0.4.2"))
		w.run(t)

		if got := w.api.created(operatorPodsPath); got != 1 {
			t.Errorf("the operators created %d scan pods, want 1", got)
		}
		if r := w.record(t, "lvfs", "node-2"); r.File != "firmware-09139-stable.xml.zst" || r.Result != resultScanned {
			t.Errorf("the record is %+v, want Scanned against the new file", r)
		}
	})
}
