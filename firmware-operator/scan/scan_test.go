package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanned(t *testing.T) {
	r := newRig(t)

	got := r.scan(context.Background())

	want := Result{
		Result: "Scanned", File: testFile, Message: "2 devices, 2 with updates",
		Devices: []Device{
			{
				Name: "System Firmware", DeviceID: "3fcf7d72a67e2a66a14eb28eac286cc8c3ace55c",
				Plugin: "uefi_capsule", GUIDs: []string{"9c13b7f1-d618-5d68-be61-6b17881014a7"},
				Version: "0.0.3.5", Vendor: "Framework",
				Releases: []Release{{
					Version: "0.0.4.2", ID: "work.frame.Laptop.RyzenAI300.BIOS.firmware", ReleaseID: "143241",
					Cabinet: &Cabinet{
						URL:    "https://fwupd.org/downloads/9c085d03d6ea515101a8519f7855bf951e7693ff4f392585bbb692275a8b27f5-Framework_Laptop_13_RyzenAI300_fwupd_4.02.cab",
						SHA256: "9c085d03d6ea515101a8519f7855bf951e7693ff4f392585bbb692275a8b27f5",
						Size:   36134500,
					},
				}},
			},
			{
				Name: "UEFI dbx", DeviceID: "362301da643102b9f38477387e2193e57abaa590", Plugin: "uefi_dbx",
				GUIDs:   []string{"1606d7b5-6eea-5abc-8a31-90c0e5cc274a", "d07ff664-b0e1-5f4e-a723-d7fbcbfcb94f", "f8ba2887-9411-5c36-9cee-88995bb39731"},
				Version: "20250902",
				Releases: []Release{
					{Version: "20260707"},
					{Version: "20260402"},
				},
			},
		},
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("the result is %s, want %s", gotJSON, wantJSON)
	}
}

func TestScanRefreshesTheCatalogUnderItsRemote(t *testing.T) {
	r := newRig(t)

	r.scan(context.Background())

	want := []string{
		"refresh", filepath.Join(r.workDir, testFile), filepath.Join(r.workDir, testFile+".jcat"), "catalog-lvfs",
	}
	if got := r.tool.calls[0]; strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("the first fwupdtool command is %v, want %v", got, want)
	}
}

func TestScanWritesTheRemotes(t *testing.T) {
	r := newRig(t)

	r.scan(context.Background())

	shipped, _ := os.ReadFile(filepath.Join(r.remotesDir, "lvfs.conf"))
	ours, _ := os.ReadFile(filepath.Join(r.remotesDir, "catalog-lvfs.conf"))
	if !strings.Contains(string(shipped), "Enabled=false") {
		t.Errorf("the shipped remote is still enabled:\n%s", shipped)
	}
	want := "[fwupd Remote]\nEnabled=true\nTitle=lvfs\nMetadataURI=" + testCatalogURL + "\n"
	if string(ours) != want {
		t.Errorf("the remote file is:\n%s\nwant:\n%s", ours, want)
	}
}

func TestScanOutcomes(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(*testing.T, *rig)
		result  string
		message string
	}{
		{
			"no updates", func(t *testing.T, r *rig) {
				r.tool.outputs["get-updates"] = toolOutput{stdout: string(fixture(t, "updates_none.json")), err: errExit2}
			},
			"Scanned", "2 devices, no updates",
		},
		{
			"nothing to update, as an error object", func(t *testing.T, r *rig) {
				r.tool.outputs["get-updates"] = toolOutput{stdout: string(fixture(t, "devices_none.json")), err: errExit2}
			},
			"Scanned", "2 devices, no updates",
		},
		{
			"no devices", func(t *testing.T, r *rig) {
				r.tool.outputs["get-devices"] = toolOutput{stdout: string(fixture(t, "devices_none.json")), err: errExit2}
			},
			"Scanned", "0 devices, no updates",
		},
		{
			"the catalog moved", func(t *testing.T, r *rig) {
				r.web[testCatalogURL+".jcat"] = jcatFor(t, "firmware-09139-stable.xml.zst", sum(testCatalog))
			},
			"CatalogMoved", "the jcat now lists firmware-09139-stable.xml.zst",
		},
		{
			"the jcat download fails", func(t *testing.T, r *rig) {
				delete(r.web, testCatalogURL+".jcat")
			},
			"Failed", "downloading the jcat: " + testCatalogURL + ".jcat returned Not Found",
		},
		{
			"the jcat is not gzip", func(t *testing.T, r *rig) {
				r.web[testCatalogURL+".jcat"] = []byte("this is not gzip data")
			},
			"Failed", "reading the jcat: gzip: invalid header",
		},
		{
			"the file download fails", func(t *testing.T, r *rig) {
				delete(r.web, testFileURL)
			},
			"Failed", "downloading " + testFile + ": " + testFileURL + " returned Not Found",
		},
		{
			"the file differs from the operator's digest", func(t *testing.T, r *rig) {
				r.cfg.sha256 = strings.Repeat("0", 64)
			},
			"Failed", "the SHA-256 of " + testFile + " is " + sum(testCatalog) + ", and the operator expects " + strings.Repeat("0", 64),
		},
		{
			"the file differs from the jcat's digest", func(t *testing.T, r *rig) {
				r.web[testCatalogURL+".jcat"] = jcatFor(t, testFile, strings.Repeat("1", 64))
			},
			"Failed", "the SHA-256 of " + testFile + " is " + sum(testCatalog) + ", and the operator expects " + sum(testCatalog),
		},
		{
			"the refresh fails", func(t *testing.T, r *rig) {
				r.tool.outputs["refresh"] = toolOutput{stderr: "starting\nfailed to verify signature\n", err: errExit2}
			},
			"Failed", "fwupdtool refresh: failed to verify signature",
		},
		{
			"get-devices fails", func(t *testing.T, r *rig) {
				r.tool.outputs["get-devices"] = toolOutput{stdout: string(fixture(t, "error.json")), err: errExit2}
			},
			"Failed", "fwupdtool get-devices: Failed to load plugin",
		},
		{
			"get-updates prints nothing", func(t *testing.T, r *rig) {
				r.tool.outputs["get-updates"] = toolOutput{stderr: "killed\n", err: errExit2}
			},
			"Failed", "fwupdtool get-updates: killed",
		},
		{
			"the remotes directory is missing", func(t *testing.T, r *rig) {
				r.remotesDir = filepath.Join(r.remotesDir, "missing")
			},
			"Failed", "writing the fwupd remote",
		},
		{
			"the work directory cannot be made", func(t *testing.T, r *rig) {
				blocker := filepath.Join(t.TempDir(), "file")
				os.WriteFile(blocker, nil, 0o644)
				r.workDir = filepath.Join(blocker, "catalog")
			},
			"Failed", "saving the catalog",
		},
		{
			"the firmware tables cannot be copied", func(t *testing.T, r *rig) {
				writeFile(t, filepath.Join(r.firmwareHost, "efi", "efivars", "BootOrder-"+efiGlobalGUID), []byte{7, 0, 0, 0})
				blocker := filepath.Join(t.TempDir(), "file")
				writeFile(t, blocker, nil)
				r.firmwareCopy = filepath.Join(blocker, "fw")
			},
			"Failed", "copying the firmware tables",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			tt.arrange(t, r)

			got := r.scan(context.Background())

			if got.Result != tt.result || !strings.HasPrefix(got.Message, tt.message) {
				t.Errorf("the result is %s %q, want %s starting with %q", got.Result, got.Message, tt.result, tt.message)
			}
			if got.File != testFile {
				t.Errorf("the result's file is %q, want %q", got.File, testFile)
			}
		})
	}
}

func TestEmitWritesOneLine(t *testing.T) {
	var out bytes.Buffer

	emit(&out, Result{Result: "Failed", Message: "a\nb", File: testFile})

	if strings.Count(out.String(), "\n") != 1 || !strings.HasSuffix(out.String(), "}\n") {
		t.Errorf("emit wrote %q, want one line ending in a newline", out.String())
	}
}

func TestConfigFromEnv(t *testing.T) {
	env := map[string]string{
		"CATALOG_NAME": "lvfs", "CATALOG_URL": testCatalogURL, "CATALOG_FILE": testFile, "CATALOG_SHA256": "abc",
	}

	got := configFromEnv(func(k string) string { return env[k] })

	want := config{name: "lvfs", url: testCatalogURL, file: testFile, sha256: "abc"}
	if got != want {
		t.Errorf("configFromEnv returned %+v, want %+v", got, want)
	}
}

func TestAScanGivesFwupdACopyOfTheFirmwareTables(t *testing.T) {
	r := newRig(t)
	writeFile(t, filepath.Join(r.firmwareHost, "efi", "efivars", "BootOrder-"+efiGlobalGUID), []byte{7, 0, 0, 0, 2, 0})
	writeFile(t, filepath.Join(r.firmwareHost, "efi", "esrt", "entries", "entry0", "fw_version"), []byte("1\n"))
	var copied []byte
	r.tool.effects = map[string]func(){"get-devices": func() {
		copied, _ = os.ReadFile(filepath.Join(r.firmwareCopy, "efi", "esrt", "entries", "entry0", "fw_version"))
	}}

	got := r.scan(context.Background())

	if got.Result != resultScanned || string(copied) != "1\n" {
		t.Errorf("the result is %+v and the copy's ESRT entry reads %q, want Scanned over a copy of the tables", got, copied)
	}
}

func TestAScanOfABIOSMachineCopiesNothing(t *testing.T) {
	r := newRig(t)

	got := r.scan(context.Background())

	if got.Result != resultScanned {
		t.Errorf("the result is %+v, want Scanned", got)
	}
}
