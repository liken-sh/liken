package main

// The parts of the test world that discovery needs. With these helpers
// a test acts as a person who applies a FirmwareCatalog, as
// machine-operator, as the kubelet, and as the scan program.

import (
	"encoding/json"
	"testing"
	"time"
)

// The boot times of a test machine before and after a reboot.
const (
	firstBoot  = "2026-10-20T01:47:55Z"
	secondBoot = "2026-10-21T06:00:00Z"
)

// putCatalog creates a FirmwareCatalog, or replaces its spec as kubectl
// apply does. A changed spec.url increments metadata.generation.
func (a *fakeAPI) putCatalog(name, address string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	object := map[string]any{
		"apiVersion": firmwareAPIVersion, "kind": catalogKind,
		"metadata": map[string]any{"name": name, "uid": "uid-" + name, "generation": 1},
		"spec":     map[string]any{"url": address},
	}
	kind := "ADDED"
	if stored, exists := a.objects[catalogsPath][name]; exists {
		kind = "MODIFIED"
		object["status"] = stored["status"]
		generation := stored["metadata"].(map[string]any)["generation"].(float64)
		if stored["spec"].(map[string]any)["url"] != address {
			generation++
		}
		object["metadata"].(map[string]any)["generation"] = generation
	}
	a.store(catalogsPath, object, kind)
}

func (a *fakeAPI) catalog(t *testing.T, name string) catalog {
	t.Helper()
	var out catalog
	a.mu.Lock()
	object := clone(a.objects[catalogsPath][name])
	a.mu.Unlock()
	a.decode(t, object, &out)
	return out
}

// putFleetMachine stores a Machine with the given status.phase and
// status.boot.time, as machine-operator reports them.
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

// onlyScan fails the test unless there is exactly one scan pod.
func (w *world) onlyScan(t *testing.T) pod {
	t.Helper()
	scans := w.api.scans(t)
	if len(scans) != 1 {
		t.Fatalf("%d scan pods, want 1", len(scans))
	}
	return scans[0]
}

func (a *fakeAPI) finishScan(t *testing.T, name, phase, log string) {
	t.Helper()
	a.finishPod(t, name, scanContainer, phase, log)
}

// finishPod acts as the kubelet and the program in a scan or rehearsal
// pod: the container writes log and the pod ends in phase.
func (a *fakeAPI) finishPod(t *testing.T, name, container, phase, log string) {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	next := clone(a.objects[operatorPodsPath][name])
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
			"name":  container,
			"state": map[string]any{"terminated": map[string]any{"exitCode": exitCode, "finishedAt": time.Now().UTC().Format(time.RFC3339)}},
		}},
	}
	a.store(operatorPodsPath, next, "MODIFIED")
}

// record fails the test when the FirmwareCatalog's status has no record
// of the machine.
func (w *world) record(t *testing.T, catalog, machine string) machineRecord {
	t.Helper()
	r, found := w.api.catalog(t, catalog).Status.record(machine)
	if !found {
		t.Fatalf("the FirmwareCatalog %s has no record of %s", catalog, machine)
	}
	return r
}

func (a *fakeAPI) discoveredObjects(t *testing.T) []firmware {
	t.Helper()
	var out []firmware
	for _, object := range a.stored(firmwaresPath) {
		var f firmware
		a.decode(t, object, &f)
		if f.Metadata.Labels[discoveredLabel] == discoveredLabelValue {
			out = append(out, f)
		}
	}
	return out
}

// resultLog returns the log of a scan: a line from fwupdtool, then the
// result as the last line.
func resultLog(result scanResult) string {
	line, _ := json.Marshal(result)
	return "fwupdtool: loading plugins\n" + string(line) + "\n"
}

// scanned returns the result of a successful scan.
func scanned(file string, devices ...scanDevice) scanResult {
	return scanResult{Result: resultScanned, Message: "the scan found the devices", File: file, Devices: devices}
}

// systemFirmware returns the Framework laptop's BIOS device at a
// version, with the releases that the catalog offers for it.
func systemFirmware(version string, offered ...string) scanDevice {
	d := scanDevice{Name: "System Firmware", DeviceID: "3fcf", Plugin: "uefi_capsule", GUIDs: []string{biosGUID}, Version: version, Vendor: "Framework"}
	for _, release := range offered {
		d.Releases = append(d.Releases, offeredRelease{Version: release, ID: frameworkBIOS().id, Urgency: "high",
			Cabinet: &cabinet{URL: "https://fwupd.org/downloads/" + release + ".cab", SHA256: "9c085d03", Size: 36134500}})
	}
	return d
}

// secureBootDBX returns the Secure Boot revocation list (dbx) device at
// a version, with the releases that the catalog offers for it.
func secureBootDBX(version string, offered ...string) scanDevice {
	d := scanDevice{Name: "UEFI dbx", DeviceID: "a1b2", Plugin: "uefi_dbx", GUIDs: []string{dbxGUID}, Version: version}
	for _, release := range offered {
		d.Releases = append(d.Releases, offeredRelease{Version: release, ID: dbx().id})
	}
	return d
}

// postedAboutCatalog returns the Events about a FirmwareCatalog, in the
// order the operator posted them.
func (w *world) postedAboutCatalog(name string) []posted {
	var out []posted
	for _, event := range w.api.recorded.About(catalogKind, name) {
		out = append(out, posted{event.Type, event.Reason})
	}
	return out
}

// discoveryWorld starts the operator with the lvfs FirmwareCatalog and a
// catalog file that offers BIOS 0.0.4.2, and one Ready machine, node-2,
// with a camera on a USB port. It returns once the first scan has
// started.
func discoveryWorld(t *testing.T) *world {
	w := startWorld(t)
	setUpDiscovery(w)
	return w
}

// setUpDiscovery is the setup of discoveryWorld, for a world whose
// operator is already running.
func setUpDiscovery(w *world) {
	w.lvfs.publish("firmware-09138-stable.xml.zst", frameworkBIOS("0.0.3.5", "0.0.4.2"), dbx("1", "2"))
	// The ResourceSlice exists before the Machine and the
	// FirmwareCatalog, as on a machine that has been running for a while.
	w.api.publish("node-2", camera("1-3", 4))
	w.settle()
	w.api.putFleetMachine("node-2", machineReady, firstBoot)
	w.settle()
	w.api.putCatalog("lvfs", lvfsURL)
	w.settle()
}

// The names of the discovered Firmware objects. A device's object is
// named for its catalog component once a catalog offers the device a
// release, and for its set of GUIDs until then. In scannedWorld the
// dbx has no offered release, so its object is dbxGUIDsName.
const (
	biosObjectName = "frame-laptop-ryzenai300-bios"
	dbxGUIDsName   = "uefi-dbx-7bc32f04"
	dbxObjectName  = "microsoft-dbx-x64"
)

// scannedWorld is discoveryWorld after node-2's scan returned its BIOS,
// which is one release behind, and its dbx, which is current.
func scannedWorld(t *testing.T) *world {
	w := discoveryWorld(t)
	w.api.finishScan(t, w.onlyScan(t).Metadata.Name, podSucceeded,
		resultLog(scanned("firmware-09138-stable.xml.zst", systemFirmware("0.0.3.5", "0.0.4.2"), secureBootDBX("2"))))
	w.settle()
	return w
}
