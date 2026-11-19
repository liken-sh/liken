package main

// These tests check how the operator creates, fills, and deletes
// discovered Firmware objects as scans finish.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// rescan reboots node-2, which makes the operator scan it again, and
// finishes that scan with a result that reports devices.
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
		changed := w.api.firmware(t, dbxGUIDsName)
		changed.Spec.Update.GUIDs = append(changed.Spec.Update.GUIDs, meGUID)
		w.api.putFirmware(changed)
		rescan(t, w)

		if got := len(w.api.stored(firmwaresPath)); got != 1 {
			t.Fatalf("%d Firmware objects remain, want only the changed one", got)
		}
		object := w.api.firmware(t, dbxGUIDsName)
		if len(object.Status.Machines) != 0 || updateCountsOf(t, &object) != (UpdateCounts{}) {
			t.Errorf("the changed object's status is %+v, want no entry and zero counts", object.Status)
		}
		if containsPosted(w.postedAbout(dbxGUIDsName), posted{"Normal", reasonDeleted}) {
			t.Errorf("the operator posted a Deleted Event about the changed object %s", dbxGUIDsName)
		}
	})
}

// An approval whose release no machine is offered any longer installs
// nothing, so the operator deletes its object with its last entry.
func TestAnApprovedObjectWithNoEntryIsDeleted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		approved := w.api.firmware(t, biosObjectName)
		approved.Spec.Update.Version = "0.0.4.2"
		approved.Spec.Update.Cabinet = &pinnedCabinet{URL: "https://fwupd.org/downloads/bios.cab", SHA256: "9c085d03"}
		w.api.putFirmware(approved)
		w.settle()
		rescan(t, w, secureBootDBX("2"))

		for _, object := range w.api.stored(firmwaresPath) {
			if object["metadata"].(map[string]any)["name"] == biosObjectName {
				t.Errorf("the approved object %s remains with no entry", biosObjectName)
			}
		}
		if !containsPosted(w.postedAbout(biosObjectName), posted{"Normal", reasonDeleted}) {
			t.Errorf("the operator posted %v about %s, want a Deleted Event", w.postedAbout(biosObjectName), biosObjectName)
		}
	})
}

// The operator deletes a discovered object with no entry when nobody
// changed it, or when its only change is an approval with no apply
// under way.
func TestWhichObjectsWithNoEntryAreDeleted(t *testing.T) {
	approval := func(edit func(*firmware)) *firmware {
		f := &firmware{Spec: firmwareSpec{Update: &update{
			Component: "work.frame.Laptop.RyzenAI300.BIOS.firmware", Version: "0.0.4.2",
			Cabinet: &pinnedCabinet{URL: "https://cabinet", SHA256: "sum"}}}}
		f.Metadata.Name, f.Metadata.Generation = biosObjectName, 2
		edit(f)
		return f
	}
	cases := []struct {
		name string
		f    *firmware
		want bool
	}{
		{"a pristine object", approval(func(f *firmware) { f.Metadata.Generation = 1 }), true},
		{"an approval", approval(func(*firmware) {}), true},
		{"an approval under the long name", approval(func(f *firmware) { f.Metadata.Name = componentName(f.Spec.Update.Component) }), true},
		{"a component a person changed", approval(func(f *firmware) { f.Spec.Update.Component = "com.example.other.firmware" }), false},
		{"GUIDs a person added", approval(func(f *firmware) { f.Spec.Update.GUIDs = []string{meGUID} }), false},
		{"an apply that waits for its turn", approval(func(f *firmware) {
			f.Status.Applies = []applyRecord{{Machine: "node-2", Result: resultWaiting}}
		}), false},
		{"an apply that writes", approval(func(f *firmware) {
			f.Status.Applies = []applyRecord{{Machine: "node-2", Result: resultWriting}}
		}), false},
		{"an apply that ended", approval(func(f *firmware) {
			f.Status.Applies = []applyRecord{{Machine: "node-2", Result: resultWritten}}
		}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := retirable(c.f); got != c.want {
				t.Errorf("retirable = %t, want %t", got, c.want)
			}
		})
	}
}

// A device that installs the newest release gets no offer, and keeps
// its entry in its component's object, with the new version.
func TestADeviceThatInstallsTheNewestReleaseKeepsItsComponent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		rescan(t, w, systemFirmware("0.0.4.2"), secureBootDBX("2"))

		bios := w.api.firmware(t, biosObjectName)
		if len(bios.Status.Machines) != 1 || bios.Status.Machines[0].Version != "0.0.4.2" || len(bios.Status.Machines[0].Releases) != 0 {
			t.Errorf("the BIOS entries are %+v, want node-2 at 0.0.4.2 with no release", bios.Status.Machines)
		}
		if updateCountsOf(t, &bios) != (UpdateCounts{Machines: 1}) {
			t.Errorf("the BIOS counts are %+v, want one machine, none behind", bios.Status.Counts)
		}
		if got := len(w.api.discoveredObjects(t)); got != 2 {
			t.Errorf("%d discovered objects, want the BIOS's component and the dbx's GUIDs", got)
		}
	})
}

// A device that a catalog offers its first release leaves the object of
// its GUIDs for its component's object, and the emptied object is
// deleted.
func TestADeviceWithAFirstReleaseMovesToItsComponent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		rescan(t, w, systemFirmware("0.0.3.5", "0.0.4.2"), secureBootDBX("1", "2"))

		names := []string{}
		for _, f := range w.api.discoveredObjects(t) {
			names = append(names, f.Metadata.Name)
		}
		slices.Sort(names)
		if want := []string{biosObjectName, dbxObjectName}; !slices.Equal(names, want) {
			t.Fatalf("the discovered objects are %v, want %v", names, want)
		}
		moved := w.api.firmware(t, dbxObjectName)
		if updateCountsOf(t, &moved) != (UpdateCounts{Machines: 1, Behind: 1}) {
			t.Errorf("the dbx component's counts are %+v, want one machine, one behind", moved.Status.Counts)
		}
	})
}

// A device whose GUIDs differ between two machines has an entry for
// each machine in the same component object, because the catalog
// offers both machines a release of that component.
func TestOneComponentOnTwoMachinesIsOneObject(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		w.api.publish("node-3", camera("1-2", 3))
		w.settle()
		w.api.putFleetMachine("node-3", machineReady, firstBoot)
		w.settle()
		for _, scan := range w.api.scans(t) {
			device := secureBootDBX("1", "2")
			if scanOf(scan).machine == "node-3" {
				device.GUIDs = append(device.GUIDs, kekGUID)
			}
			w.api.finishScan(t, scan.Metadata.Name, podSucceeded, resultLog(scanned("firmware-09138-stable.xml.zst", device)))
		}
		w.settle()

		objects := w.api.discoveredObjects(t)
		if len(objects) != 1 || objects[0].Metadata.Name != dbxObjectName {
			t.Fatalf("the discovered objects are %+v, want only the dbx component's", objects)
		}
		if got := updateCountsOf(t, &objects[0]); got != (UpdateCounts{Machines: 2, Behind: 2}) {
			t.Errorf("the counts are %+v, want two machines, both behind", got)
		}
	})
}

// A device offered releases of two components has its entry in the
// object of the newest release's component, and the operator posts
// ManyComponents.
func TestReleasesOfTwoComponentsPostAWarning(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		device := systemFirmware("0.0.3.5", "0.0.4.2")
		device.Releases = append(device.Releases, offeredRelease{Version: "0.0.3.9", ID: "work.frame.Laptop.Other.firmware"})
		rescan(t, w, device, secureBootDBX("2"))

		want := []posted{{"Normal", reasonDiscovered}, {"Normal", reasonUpdateAvailable}, {"Warning", reasonManyComponents}}
		if got := w.postedAbout(biosObjectName); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}

// A status write that the API server refuses keeps the scan pod, and
// the next pass applies its result again. The ManyComponents Warning is
// about the scan's result, so the retry does not post it again.
func TestARetriedScanPostsItsWarningOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		device := systemFirmware("0.0.3.5", "0.0.4.2")
		device.Releases = append(device.Releases, offeredRelease{Version: "0.0.3.9", ID: "work.frame.Laptop.Other.firmware"})
		w.api.putFleetMachine("node-2", machineReady, secondBoot)
		w.settle()
		w.api.refuseStatusWritesTo(firmwaresPath, 1)
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded, resultLog(scanned("firmware-09138-stable.xml.zst", device, secureBootDBX("2"))))
		w.settle()
		time.Sleep(retryDelay)
		w.settle()

		var counts []int32
		for _, event := range w.api.recorded.About(firmwareKind, biosObjectName) {
			if event.Reason == reasonManyComponents {
				counts = append(counts, event.Count)
			}
		}
		if !slices.Equal(counts, []int32{1}) {
			t.Errorf("the ManyComponents Events have the counts %v, want one Event with the count 1", counts)
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
		if got := w.postedAbout(biosObjectName); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}

// A deleted Machine leaves no record and no entry, and the operator
// deletes a discovered object when its last entry is removed.
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

// A component's object is named by its ID with no top-level domain and
// no "firmware" part.
func TestTheShortComponentName(t *testing.T) {
	cases := []struct {
		name, id, want string
	}{
		{"the dbx", "com.microsoft.dbx.x64.firmware", "microsoft-dbx-x64"},
		{"capitals", "work.frame.Laptop.RyzenAI300.BIOS.firmware", "frame-laptop-ryzenai300-bios"},
		{"no firmware part", "org.example.dock", "example-dock"},
		{"one part", "firmware", "firmware"},
		{"a long ID", "com." + strings.Repeat("a", 70), strings.Repeat("a", 63)},
		{"no letters", "com.__", "component"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shortComponentName(c.id); got != c.want {
				t.Errorf("shortComponentName = %q, want %q", got, c.want)
			}
		})
	}
}

// A component's longer name is its ID in lower case, with each part
// made a valid DNS label.
func TestTheComponentName(t *testing.T) {
	cases := []struct {
		name, id, want string
	}{
		{"a plain ID", "org.linuxfoundation.dbx.x64.firmware", "org.linuxfoundation.dbx.x64.firmware"},
		{"capitals", "work.frame.Laptop.RyzenAI300.BIOS.firmware", "work.frame.laptop.ryzenai300.bios.firmware"},
		{"punctuation", "com.example.Dock_USB-C..firmware", "com.example.dock-usb-c.firmware"},
		{"a long part", strings.Repeat("a", 70) + ".firmware", strings.Repeat("a", 63) + ".firmware"},
		{"no letters", "..__", "component"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := componentName(c.id); got != c.want {
				t.Errorf("componentName = %q, want %q", got, c.want)
			}
		})
	}
}

// A set of GUIDs gives the same model key in any order or case.
func TestTheModelKeyIgnoresOrderAndCase(t *testing.T) {
	if modelKey([]string{"B", "a", "b"}) != modelKey([]string{"a", "b"}) {
		t.Error("the model key differs for one set of GUIDs")
	}
}

// A device that reports no GUID gets no group and no object.
func TestADeviceWithNoGUIDIsNoModel(t *testing.T) {
	s := entriesOf(machineRecord{Machine: "node-2"}, "lvfs", []scanDevice{{Name: "Unknown"}, systemFirmware("0.0.3.5")}, nil)
	if len(s.byGroup) != 1 || len(s.byGroup[groupKey{guids: modelKey([]string{biosGUID})}]) != 1 {
		t.Errorf("the groups are %+v, want only the BIOS", s.byGroup)
	}
}

// When a person changes a discovered object's GUIDs, the object keeps
// its name. A later scan of the device that the name was made from
// records the machine's entry in that object.
func TestAGroupWhoseObjectAPersonChangedKeepsItsName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		changed := w.api.firmware(t, dbxGUIDsName)
		changed.Spec.Update.GUIDs = []string{meGUID}
		w.api.putFirmware(changed)
		rescan(t, w, secureBootDBX("2"))

		object := w.api.firmware(t, dbxGUIDsName)
		if len(object.Status.Machines) != 1 || !slices.Equal(object.Spec.Update.GUIDs, []string{meGUID}) {
			t.Errorf("the object is %+v, want the person's GUIDs and node-2's entry", object)
		}
	})
}

// A group whose short name another Firmware uses, such as a person's
// upload rule, gets its object under the longer name, and the other
// Firmware keeps its spec.
func TestATakenShortNameMovesTheObjectToTheLongerName(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		w.api.putFirmware(named(qhy5lii(), biosObjectName))
		w.settle()
		w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
			resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"))))
		w.settle()

		bios := w.api.firmware(t, "work.frame.laptop.ryzenai300.bios.firmware")
		if bios.Spec.Update.Component != frameworkBIOS().id || len(bios.Status.Machines) != 1 {
			t.Errorf("the BIOS object is %+v, want the component and node-2's entry", bios)
		}
		if object := w.api.firmware(t, biosObjectName); object.Spec.Upload == nil || len(object.Status.Machines) != 0 {
			t.Errorf("the upload rule is %+v, want its upload and no entry", object)
		}
	})
}
