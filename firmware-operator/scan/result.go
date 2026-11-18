package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// The results of a scan. CatalogMoved means the jcat lists a different
// catalog file than CATALOG_FILE, so the operator reads the jcat again
// and starts a new scan. The operator does not retry a Failed scan
// until the machine's boot, its devices, the catalog file, or the scan
// image changes.
const (
	resultScanned      = "Scanned"
	resultCatalogMoved = "CatalogMoved"
	resultFailed       = "Failed"
)

// Result is the line of JSON that the scan prints last. The operator
// reads the last line of the pod's log that parses as JSON with a
// non-empty result.
type Result struct {
	Result  string   `json:"result"`
	Message string   `json:"message"`
	File    string   `json:"file"`
	Devices []Device `json:"devices,omitempty"`
}

// Device is a firmware device that fwupd can update, with the releases
// that the catalog offers for it.
type Device struct {
	Name     string    `json:"name"`
	DeviceID string    `json:"deviceId"`
	Plugin   string    `json:"plugin,omitempty"`
	GUIDs    []string  `json:"guids,omitempty"`
	Version  string    `json:"version,omitempty"`
	Vendor   string    `json:"vendor,omitempty"`
	Releases []Release `json:"releases,omitempty"`
}

// Release is one version of a device's firmware that the catalog offers.
type Release struct {
	Version   string   `json:"version"`
	ID        string   `json:"id,omitempty"`
	ReleaseID string   `json:"releaseId,omitempty"`
	Urgency   string   `json:"urgency,omitempty"`
	Cabinet   *Cabinet `json:"cabinet,omitempty"`
}

// Cabinet is the .cab file that holds a release's firmware. An approval
// on a Firmware pins its URL and SHA-256.
type Cabinet struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
}

func failed(file, format string, args ...any) Result {
	return Result{Result: resultFailed, File: file, Message: fmt.Sprintf(format, args...)}
}

// emit writes a scan's or a rehearsal's result as one line of JSON.
// json.Encoder escapes the newlines inside strings.
func emit(w io.Writer, r any) error {
	return json.NewEncoder(w).Encode(r)
}

// summary is the scan's message in the FirmwareCatalog's status.
func summary(devices []Device) string {
	withUpdates := 0
	for _, d := range devices {
		if len(d.Releases) > 0 {
			withUpdates++
		}
	}
	noun := "devices"
	if len(devices) == 1 {
		noun = "device"
	}
	if withUpdates == 0 {
		return fmt.Sprintf("%d %s, no updates", len(devices), noun)
	}
	return fmt.Sprintf("%d %s, %d with updates", len(devices), noun, withUpdates)
}
