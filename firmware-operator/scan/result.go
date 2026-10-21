package main

import (
	"encoding/json"
	"fmt"
	"io"
)

// The three outcomes of a scan. The operator reads the result to decide
// what to record and whether to run the scan again: Scanned is a full
// answer, CatalogMoved tells the operator to read the jcat again at
// once, and Failed waits for the machine or the catalog to change.
const (
	resultScanned      = "Scanned"
	resultCatalogMoved = "CatalogMoved"
	resultFailed       = "Failed"
)

// Result is the one line of JSON that the scan prints. The operator
// takes the last line of the pod's log that parses as an object with a
// result key, so this line is the last thing the program writes.
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

// Cabinet is the file that carries a release's payload. Plan 05
// downloads it and checks the SHA-256 before it applies anything.
type Cabinet struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size,omitempty"`
}

func failed(file, format string, args ...any) Result {
	return Result{Result: resultFailed, File: file, Message: fmt.Sprintf(format, args...)}
}

// emit writes a scan's or a rehearsal's result as one line.
// json.Encoder ends the line with a newline and never writes one
// inside, so the line is whole.
func emit(w io.Writer, r any) error {
	return json.NewEncoder(w).Encode(r)
}

// summary describes a scan for a person who reads the FirmwareCatalog's status.
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
