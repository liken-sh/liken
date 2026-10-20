package main

// These tests follow the discovered Firmware objects across scans: an
// entry leaves when its machine's next scan omits the device or its
// Machine is deleted, an object with no entry is deleted unless a
// person changed its spec, and a machine's newest offered release
// posts UpdateAvailable when it changes.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// rescan reboots node-2 and answers its next scan with the devices.
func rescan(t *testing.T, w *world, devices ...scanDevice) {
	t.Helper()
	w.api.putFleetMachine("node-2", machineReady, secondBoot)
	w.settle()
	w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded, resultLog(scanned("firmware-09138-stable.xml.zst", devices...)))
	w.settle()
}

// A discovered object whose last entry leaves is deleted. An object
// whose spec a person changed stays, with no entry.
func TestAnObjectWithNoEntryIsDeletedUnlessChanged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		dbxName := discoveredName("UEFI dbx", []string{dbxGUID})
		changed := w.api.firmware(t, dbxName)
		changed.Spec.Update.GUIDs = append(changed.Spec.Update.GUIDs, meGUID)
		w.api.putFirmware(changed)
		rescan(t, w)

		if got := len(w.api.held(firmwaresPath)); got != 1 {
			t.Fatalf("%d Firmware objects remain, want only the changed one", got)
		}
		kept := w.api.firmware(t, dbxName)
		if len(kept.Status.Machines) != 0 || kept.Status.Counts != (counts{}) {
			t.Errorf("the changed object's status is %+v, want no entry and zero counts", kept.Status)
		}
	})
}

// A scan that still reports a device keeps its object, and replaces
// the machine's entry with the new version.
func TestARescanReplacesTheMachinesEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		rescan(t, w, systemFirmware("0.0.4.2"), secureBootDBX("2"))

		bios := w.api.firmware(t, discoveredName("System Firmware", []string{biosGUID}))
		if len(bios.Status.Machines) != 1 || bios.Status.Machines[0].Version != "0.0.4.2" || len(bios.Status.Machines[0].Releases) != 0 {
			t.Errorf("the BIOS entries are %+v, want node-2 at 0.0.4.2 with no release", bios.Status.Machines)
		}
		if bios.Status.Counts != (counts{Machines: 1}) {
			t.Errorf("the BIOS counts are %+v, want one machine, none behind", bios.Status.Counts)
		}
	})
}

// UpdateAvailable posts when a machine's newest offered release
// changes, and not when a scan offers the same one again.
func TestUpdateAvailablePostsOnlyForANewRelease(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		rescan(t, w, systemFirmware("0.0.3.5", "0.0.4.2"))
		w.api.putFleetMachine("node-2", machineReady, firstBoot)
		w.settle()
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
			resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.3", "0.0.4.2"))))
		w.settle()

		want := []posted{{"Normal", reasonDiscovered}, {"Normal", reasonUpdateAvailable}, {"Normal", reasonUpdateAvailable}}
		if got := w.postedAbout(discoveredName("System Firmware", []string{biosGUID})); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}

// A deleted Machine leaves no record and no entry, and its discovered
// objects go with the last entry.
func TestADeletedMachineLeavesNoEntry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		w.api.deleteObject(machinesPath, "node-2")
		w.settle()

		if got := w.api.catalog(t, "lvfs").Status.Machines; len(got) != 0 {
			t.Errorf("the FirmwareCatalog's records are %+v, want none", got)
		}
		if got := w.api.discoveredObjects(t); len(got) != 0 {
			t.Errorf("the discovered objects are %+v, want none", got)
		}
	})
}

// A machine that is off keeps its entries.
func TestAMachineThatIsOffKeepsItsEntries(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		w.api.putFleetMachine("node-2", "Lost", firstBoot)
		w.settle()

		if got := len(w.api.discoveredObjects(t)); got != 2 {
			t.Errorf("%d discovered objects, want both to stay", got)
		}
	})
}

// Two machines with one model share one object, which counts both.
func TestOneModelOnTwoMachinesIsOneObject(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		w.api.publish("node-3", camera("1-2", 3))
		w.settle()
		w.api.putFleetMachine("node-3", machineReady, firstBoot)
		w.settle()
		for _, scan := range w.api.scans(t) {
			device := systemFirmware("0.0.3.5", "0.0.4.2")
			if scanOf(scan).machine == "node-3" {
				device = systemFirmware("0.0.4.2")
			}
			w.api.finishScan(t, scan.Metadata.Name, podSucceeded, resultLog(scanned("firmware-09138-stable.xml.zst", device)))
		}
		w.settle()

		objects := w.api.discoveredObjects(t)
		if len(objects) != 1 {
			t.Fatalf("%d discovered objects, want 1", len(objects))
		}
		if got := objects[0].Status.Counts; got != (counts{Machines: 2, Behind: 1}) {
			t.Errorf("the counts are %+v, want two machines, one behind", got)
		}
	})
}

// A discovered object's name is the device's name as a DNS label and
// a hash of the GUIDs.
func TestTheDiscoveredName(t *testing.T) {
	cases := []struct {
		name, device, wantPrefix string
	}{
		{"a plain name", "System Firmware", "system-firmware-"},
		{"punctuation", "Samsung SSD 980 PRO (1TB)", "samsung-ssd-980-pro-1tb-"},
		{"no letters", "!!", "device-"},
		{"a long name", strings.Repeat("a", 70), strings.Repeat("a", 54) + "-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := discoveredName(c.device, []string{biosGUID})
			if len(got) > 63 || !strings.HasPrefix(got, c.wantPrefix) || len(got) != len(c.wantPrefix)+8 {
				t.Errorf("discoveredName = %q, want %q and eight hex digits", got, c.wantPrefix)
			}
		})
	}
}

// One set of GUIDs in any order or case names one model.
func TestTheModelKeyIgnoresOrderAndCase(t *testing.T) {
	if modelKey([]string{"B", "a", "b"}) != modelKey([]string{"a", "b"}) {
		t.Error("the model key differs for one set of GUIDs")
	}
}

// A device that reports no GUID names no model, and gets no object.
func TestADeviceWithNoGUIDIsNoModel(t *testing.T) {
	s := entriesOf(machineRecord{Machine: "node-2"}, "lvfs", []scanDevice{{Name: "Unknown"}, systemFirmware("0.0.3.5")})
	if len(s.byModel) != 1 || len(s.byModel[modelKey([]string{biosGUID})]) != 1 {
		t.Errorf("the models are %+v, want only the BIOS", s.byModel)
	}
}

// A person who changed a discovered object's GUIDs keeps the object
// under its name. A scan that reports the model of that name again
// meets the object, and records the machine's entry in it.
func TestAModelWhoseObjectAPersonChangedKeepsItsName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		name := discoveredName("System Firmware", []string{biosGUID})
		changed := w.api.firmware(t, name)
		changed.Spec.Update.GUIDs = []string{meGUID}
		w.api.putFirmware(changed)
		rescan(t, w, systemFirmware("0.0.3.5", "0.0.4.2"))

		kept := w.api.firmware(t, name)
		if len(kept.Status.Machines) != 1 || !slices.Equal(kept.Spec.Update.GUIDs, []string{meGUID}) {
			t.Errorf("the object is %+v, want the person's GUIDs and node-2's entry", kept)
		}
	})
}
