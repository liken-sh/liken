package main

// These tests read the scan pod the operator creates for a Ready
// machine with no record, and the record and the discovered Firmware
// objects that the pod's result becomes.

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// A Ready machine with no record gets one scan pod on its node, which
// runs the scan image with the catalog's file and its SHA-256.
func TestAMachineWithNoRecordGetsAScanPod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := discoveryWorld(t)
		scan := w.onlyScan(t)
		spec, container := scan.Spec, scan.Spec.Containers[0]
		c := w.api.catalog(t, "lvfs")
		attempt := scanAttempt{catalog: "lvfs", machine: "node-2", boot: firstBoot, devices: deviceSetHash([]string{camera("1-3", 4).name()}),
			file: "firmware-09138-stable.xml.zst"}
		env := []envVar{
			{catalogNameVariable, "lvfs"}, {catalogURLVariable, lvfsURL},
			{catalogFileVariable, "firmware-09138-stable.xml.zst"}, {catalogSHA256Variable, c.Status.SHA256},
		}

		checks := []struct {
			name string
			ok   bool
		}{
			{"the pod's name", scan.Metadata.Name == scanPodName(attempt)},
			{"the node", spec.NodeName == "node-2"},
			{"the tolerations", slices.Equal(spec.Tolerations, []toleration{{Operator: "Exists"}})},
			{"the restart policy", spec.RestartPolicy == "Never"},
			{"the deadline", *spec.ActiveDeadlineSeconds == 600},
			{"the grace period", *spec.TerminationGracePeriodSeconds == 5},
			{"the owner", slices.Equal(scan.Metadata.OwnerReferences, []ownerReference{{firmwareAPIVersion, catalogKind, "lvfs", "uid-lvfs"}})},
			{"the label", scan.Metadata.Labels[scanLabel] == scanLabelValue},
			{"the record", scanOf(scan) == attempt},
			{"the image", container.Image == testScanImage},
			{"the environment", slices.Equal(container.Env, env)},
			{"a privileged container", *container.SecurityContext.Privileged},
			{"no claim", len(spec.ResourceClaims) == 0},
			{"no token", !*spec.AutomountServiceAccountToken},
			{"no service links", !*spec.EnableServiceLinks},
		}
		// t.Run is not allowed inside a bubble, so each check names
		// itself in its error.
		for _, c := range checks {
			if !c.ok {
				t.Errorf("%s is wrong in the scan pod %+v", c.name, scan)
			}
		}
		r := w.record(t, "lvfs", "node-2")
		if r.Result != resultRunning || !attempt.is(r) {
			t.Errorf("the record is %+v, want Running for the attempt %+v", r, attempt)
		}
		want := []posted{{"Normal", reasonFetched}, {"Normal", reasonScanStarted}}
		if got := w.postedAboutCatalog("lvfs"); !slices.Equal(got, want) {
			t.Errorf("the Events are %v, want %v", got, want)
		}
	})
}

// A scan's result becomes the machine's record, with the GUIDs of its
// devices, and one discovered Firmware for each group: the BIOS's
// catalog component and the dbx's set of GUIDs. Then the operator
// deletes the pod.
func TestAScanResultBecomesTheRecordAndTheDiscoveredObjects(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)

		r := w.record(t, "lvfs", "node-2")
		if r.Result != resultScanned || !slices.Equal(r.GUIDs, sortedSet([]string{biosGUID, dbxGUID})) || r.Releases == "" {
			t.Errorf("the record is %+v, want Scanned with both GUIDs and a releases hash", r)
		}
		objects := w.api.discoveredObjects(t)
		names := []string{}
		for _, f := range objects {
			names = append(names, f.Metadata.Name)
		}
		want := []string{biosObjectName, dbxGUIDsName}
		slices.Sort(want)
		slices.Sort(names)
		if !slices.Equal(names, want) {
			t.Fatalf("the discovered objects are %v, want %v", names, want)
		}
		bios := w.api.firmware(t, biosObjectName)
		entry := machineEntry{Machine: "node-2", Device: "System Firmware", DeviceID: "3fcf", Version: "0.0.3.5", Catalog: "lvfs",
			File: "firmware-09138-stable.xml.zst", Releases: systemFirmware("0.0.3.5", "0.0.4.2").Releases, Time: r.Time}
		if len(bios.Status.Machines) != 1 || !entriesEqual(bios.Status.Machines[0], entry) {
			t.Errorf("the BIOS entries are %+v, want %+v", bios.Status.Machines, entry)
		}
		if bios.Spec.Update.Component != frameworkBIOS().id || bios.Spec.Update.GUIDs != nil || bios.Spec.Selector != "" || bios.Spec.Upload != nil {
			t.Errorf("the BIOS spec is %+v, want only update.component", bios.Spec)
		}
		if got := updateCountsOf(t, &bios); got != (UpdateCounts{Machines: 1, Behind: 1}) {
			t.Errorf("the BIOS counts are %+v, want one machine, one behind", got)
		}
		dbxObject := w.api.firmware(t, dbxGUIDsName)
		if !slices.Equal(dbxObject.Spec.Update.GUIDs, []string{dbxGUID}) || dbxObject.Spec.Update.Component != "" {
			t.Errorf("the dbx spec is %+v, want only update.guids", dbxObject.Spec.Update)
		}
		if got := updateCountsOf(t, &dbxObject); got != (UpdateCounts{Machines: 1}) {
			t.Errorf("the dbx counts are %+v, want one machine, none behind", got)
		}
		if got := len(w.api.scans(t)); got != 0 {
			t.Errorf("%d scan pods remain, want none", got)
		}
		wantBIOS := []posted{{"Normal", reasonDiscovered}, {"Normal", reasonUpdateAvailable}}
		if got := w.postedAbout(bios.Metadata.Name); !slices.Equal(got, wantBIOS) {
			t.Errorf("the BIOS Events are %v, want %v", got, wantBIOS)
		}
		wantCatalog := []posted{{"Normal", reasonFetched}, {"Normal", reasonScanStarted}, {"Normal", reasonScanned}}
		if got := w.postedAboutCatalog("lvfs"); !slices.Equal(got, wantCatalog) {
			t.Errorf("the FirmwareCatalog Events are %v, want %v", got, wantCatalog)
		}
	})
}

// entriesEqual compares two entries, release by release.
func entriesEqual(a, b machineEntry) bool {
	return string(mustJSON(a)) == string(mustJSON(b))
}

// A discovered object is an update rule, so the upload pass leaves it
// alone: it gets no Valid condition and no upload pod.
func TestTheUploadPassSkipsADiscoveredObject(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := scannedWorld(t)
		w.api.publish("node-2", camera("1-3", 4), bareChip("1-4", 9))
		w.settle()

		bios := w.api.firmware(t, biosObjectName)
		if len(bios.Status.Conditions) != 0 || len(bios.Status.Devices) != 0 {
			t.Errorf("the discovered object's status is %+v, want no condition and no upload entry", bios.Status)
		}
		if got := w.api.created(uploadPodsPath); got != 2 {
			t.Errorf("the operator created %d pods, want the two scan pods and no upload pod", got)
		}
	})
}

// A scan pod's name holds the catalog and the machine for a person to
// read, and stays a DNS label however long they are.
func TestTheScanPodsNameIsADNSLabel(t *testing.T) {
	cases := []struct {
		name, catalog, machine, wantPrefix string
	}{
		{"short names", "lvfs", "node-2", "lvfs-node-2-"},
		{"a long machine name", "lvfs", strings.Repeat("m", 60), "lvfs-" + strings.Repeat("m", 45) + "-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := scanPodName(scanAttempt{catalog: c.catalog, machine: c.machine, boot: firstBoot, devices: "d", file: "f"})
			if len(got) > 63 || !strings.HasPrefix(got, c.wantPrefix) || strings.Contains(got, "--") {
				t.Errorf("scanPodName = %q, want at most 63 characters that start with %q", got, c.wantPrefix)
			}
		})
	}
}
