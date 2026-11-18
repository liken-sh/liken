package main

// A FirmwareCatalog is one source of firmware releases, such as LVFS.
// The operator writes its whole status: the catalog file that the jcat
// lists, the Ready condition, and each machine's last scan against the
// catalog. A write to the status subresource replaces the whole status,
// so catalogStatus has every status field, and a field missing from it
// would be erased by each write.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

const (
	catalogKind  = "FirmwareCatalog"
	catalogsPath = "/apis/firmware.liken.sh/v1alpha1/firmwarecatalogs"
)

func catalogPath(name string) string { return catalogsPath + "/" + name }

type catalog struct {
	APIVersion string        `json:"apiVersion"`
	Kind       string        `json:"kind"`
	Metadata   firmwareMeta  `json:"metadata"`
	Spec       catalogSpec   `json:"spec"`
	Status     catalogStatus `json:"status,omitzero"`
}

func (c *catalog) GetObjectMeta() informer.Meta { return &c.Metadata }

type catalogSpec struct {
	// URL is the catalog's unversioned URL. The jcat is at the same URL
	// with .jcat added, and the versioned file is in the same
	// directory, under the name that the jcat gives.
	URL string `json:"url"`
	// Trust names the ConfigMap with the signer's certificate, for a
	// catalog that LVFS did not sign (catalogtrust.go).
	Trust *catalogTrust `json:"trust,omitempty"`
}

type catalogStatus struct {
	File       string          `json:"file,omitempty"`
	SHA256     string          `json:"sha256,omitempty"`
	ETag       string          `json:"etag,omitempty"`
	Checked    string          `json:"checked,omitempty"`
	Conditions []condition     `json:"conditions,omitempty"`
	Machines   []machineRecord `json:"machines,omitempty"`
}

// machineRecord is one machine's last scan against the catalog. Boot,
// Devices, File, and Image identify a scan attempt (scanAttempt.is),
// and the operator runs each attempt once. The operator reads these
// records from status after a restart, so it keeps no copy of its own.
type machineRecord struct {
	Machine string `json:"machine"`
	// Boot is the Machine's status.boot.time when the scan started.
	Boot string `json:"boot,omitempty"`
	// Devices is a hash of the names of the devices in the machine's
	// slice (deviceSetHash).
	Devices string `json:"devices"`
	File    string `json:"file"`
	// Image is the scan image that ran the scan. A new image can find
	// devices and releases that the old one did not, so a record made by
	// another image is out of date.
	Image string `json:"image,omitempty"`
	// Releases is a hash of the catalog's releases for GUIDs in File
	// (catalogIndex.releasesHash). When a new catalog file has the same
	// hash, the operator moves the record to the new file without a
	// scan. An empty hash means the operator could not compute it, so
	// the next new file scans the machine again.
	Releases string `json:"releases,omitempty"`
	// GUIDs are the GUIDs of every updatable device that the machine
	// reported in its last scan that completed.
	GUIDs   []string `json:"guids,omitempty"`
	Result  string   `json:"result"`
	Message string   `json:"message,omitempty"`
	Time    string   `json:"time"`
}

// The results that only a scan record has. A scan record also uses
// resultRunning and resultFailed from firmware.go.
const (
	resultScanned      = "Scanned"
	resultCatalogMoved = "CatalogMoved"
)

// ready is the condition type of a FirmwareCatalog. It is True when the
// operator has read the jcat and the file that the jcat lists.
const ready = "Ready"

const (
	reasonFetched          = "Fetched"
	reasonJcatUnreadable   = "JcatUnreadable"
	reasonFileUnreadable   = "FileUnreadable"
	reasonChecksumMismatch = "ChecksumMismatch"
)

func (s catalogStatus) ready() condition {
	c, _ := conditions.Find(s.Conditions, ready)
	return c
}

func (s catalogStatus) record(machine string) (machineRecord, bool) {
	for _, r := range s.Machines {
		if r.Machine == machine {
			return r, true
		}
	}
	return machineRecord{}, false
}

// reference identifies the FirmwareCatalog as the object of its Events.
func (c *catalog) reference() eventReference {
	return eventReference{APIVersion: firmwareAPIVersion, Kind: catalogKind, Name: c.Metadata.Name, UID: c.Metadata.UID}
}
