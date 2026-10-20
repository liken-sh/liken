package main

// The part of the test world that discovery needs: a Catalog, Machines
// with a phase and a boot time, the scan pods, and the result line a
// scan writes. A test plays a person, machine-operator, the kubelet,
// and the scan program.

import (
	"encoding/json"
	"testing"
	"time"
)

// The boot times of a test machine, before and after a reboot.
const (
	firstBoot  = "2026-10-20T01:47:55Z"
	secondBoot = "2026-10-21T06:00:00Z"
)

// putCatalog creates a Catalog, or replaces its spec the way kubectl
// apply does. A change of the spec moves the generation.
func (a *fakeAPI) putCatalog(name, address string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object := map[string]any{
		"apiVersion": firmwareAPIVersion, "kind": catalogKind,
		"metadata": map[string]any{"name": name, "uid": "uid-" + name, "generation": 1},
		"spec":     map[string]any{"url": address},
	}
	kind := "ADDED"
	if held, exists := a.objects[catalogsPath][name]; exists {
		kind = "MODIFIED"
		object["status"] = held["status"]
		generation := held["metadata"].(map[string]any)["generation"].(float64)
		if held["spec"].(map[string]any)["url"] != address {
			generation++
		}
		object["metadata"].(map[string]any)["generation"] = generation
	}
	a.store(catalogsPath, object, kind)
}

// catalog answers the stored Catalog.
func (a *fakeAPI) catalog(t *testing.T, name string) catalog {
	t.Helper()
	var out catalog
	a.mu.Lock()
	object := clone(a.objects[catalogsPath][name])
	a.mu.Unlock()
	a.decode(t, object, &out)
	return out
}

// putFleetMachine stores a Machine in a phase, booted at a time, the way
// the OS reports it.
func (a *fakeAPI) putFleetMachine(name, phase, boot string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	kind := "ADDED"
	if _, exists := a.objects[machinesPath][name]; exists {
		kind = "MODIFIED"
	}
	a.store(machinesPath, map[string]any{
		"apiVersion": "liken.sh/v1alpha1", "kind": "Machine",
		"metadata": map[string]any{"name": name, "uid": "uid-" + name},
		"status":   map[string]any{"phase": phase, "boot": map[string]any{"time": boot}},
	}, kind)
}

// scans answers every stored scan pod.
func (a *fakeAPI) scans(t *testing.T) []pod {
	t.Helper()
	var out []pod
	for _, p := range a.uploads(t) {
		if p.Metadata.Labels[scanLabel] == scanLabelValue {
			out = append(out, p)
		}
	}
	return out
}

// onlyScan answers the one scan pod, and fails the test when there is
// not exactly one.
func (w *world) onlyScan(t *testing.T) pod {
	t.Helper()
	scans := w.api.scans(t)
	if len(scans) != 1 {
		t.Fatalf("%d scan pods, want 1", len(scans))
	}
	return scans[0]
}

// finishScan plays the kubelet and the scan program: the scan pod wrote
// the log and ended in the phase.
func (a *fakeAPI) finishScan(t *testing.T, name, phase, log string) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[uploadPodsPath][name])
	if next == nil {
		t.Fatalf("there is no scan pod %s", name)
	}
	exitCode := 0
	if phase == podFailed {
		exitCode = 1
	}
	a.logs[name] = log
	next["status"] = map[string]any{
		"phase": phase,
		"containerStatuses": []any{map[string]any{
			"name":  scanContainer,
			"state": map[string]any{"terminated": map[string]any{"exitCode": exitCode, "finishedAt": time.Now().UTC().Format(time.RFC3339)}},
		}},
	}
	a.store(uploadPodsPath, next, "MODIFIED")
}

// record answers a machine's record in a Catalog's status, and fails
// the test when there is none.
func (w *world) record(t *testing.T, catalog, machine string) machineRecord {
	t.Helper()
	r, held := w.api.catalog(t, catalog).Status.record(machine)
	if !held {
		t.Fatalf("the Catalog %s holds no record of %s", catalog, machine)
	}
	return r
}

// discoveredObjects answers every stored discovered Firmware.
func (a *fakeAPI) discoveredObjects(t *testing.T) []firmware {
	t.Helper()
	var out []firmware
	for _, object := range a.held(firmwaresPath) {
		var f firmware
		a.decode(t, object, &f)
		if f.Metadata.Labels[discoveredLabel] == discoveredLabelValue {
			out = append(out, f)
		}
	}
	return out
}

// resultLog is the log of a scan that answered: a line fwupd wrote, and
// the result line last.
func resultLog(result scanResult) string {
	line, _ := json.Marshal(result)
	return "fwupdtool: loading plugins\n" + string(line) + "\n"
}

// scanned is the result of a scan against a file that found devices.
func scanned(file string, devices ...scanDevice) scanResult {
	return scanResult{Result: resultScanned, Message: "the scan found the devices", File: file, Devices: devices}
}

// systemFirmware is the Framework's BIOS at a version, with the
// releases the catalog offers for it.
func systemFirmware(version string, offered ...string) scanDevice {
	d := scanDevice{Name: "System Firmware", DeviceID: "3fcf", Plugin: "uefi_capsule", GUIDs: []string{biosGUID}, Version: version, Vendor: "Framework"}
	for _, release := range offered {
		d.Releases = append(d.Releases, offeredRelease{Version: release, ID: frameworkBIOS().id, Urgency: "high",
			Cabinet: &cabinet{URL: "https://fwupd.org/downloads/" + release + ".cab", SHA256: "9c085d03", Size: 36134500}})
	}
	return d
}

// secureBootDBX is the Secure Boot revocation list at a version.
func secureBootDBX(version string, offered ...string) scanDevice {
	d := scanDevice{Name: "UEFI dbx", DeviceID: "a1b2", Plugin: "uefi_dbx", GUIDs: []string{dbxGUID}, Version: version}
	for _, release := range offered {
		d.Releases = append(d.Releases, offeredRelease{Version: release, ID: dbx().id})
	}
	return d
}

// postedAboutCatalog answers what the operator posted about one
// Catalog, in the order it posted it.
func (w *world) postedAboutCatalog(name string) []posted {
	var out []posted
	for _, event := range w.api.recorded.About(catalogKind, name) {
		out = append(out, posted{event.Type, event.Reason})
	}
	return out
}

// discoveryWorld runs the operator with the lvfs Catalog at a file that
// offers BIOS 0.0.4.2, and one Ready machine, node-2, with a camera on
// a USB port. It answers once the first scan has started.
func discoveryWorld(t *testing.T) *world {
	w := startWorld(t)
	w.lvfs.publish("firmware-09138-stable.xml.zst", frameworkBIOS("0.0.3.5", "0.0.4.2"), dbx("1", "2"))
	// The machine's slice reaches the operator before the Machine and
	// the Catalog, the way it does on a cluster where the machine has
	// run a while.
	w.api.publish("node-2", camera("1-3", 4))
	w.settle()
	w.api.putFleetMachine("node-2", machineReady, firstBoot)
	w.settle()
	w.api.putCatalog("lvfs", lvfsURL)
	w.settle()
	return w
}

// scannedWorld is discoveryWorld after node-2's scan answered with its
// BIOS, behind by one release, and its dbx, current.
func scannedWorld(t *testing.T) *world {
	w := discoveryWorld(t)
	w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
		resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"), secureBootDBX("2"))))
	w.settle()
	return w
}
