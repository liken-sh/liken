package main

// The Catalog kind, as the operator reads and writes it. A Catalog is
// one source of firmware releases, such as LVFS. The operator writes
// its whole status: the catalog file the jcat names, the Ready
// condition, and the record of each machine's last scan against it. A
// write to the status subresource replaces the whole status, so the
// struct holds every status field.

import (
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

const (
	catalogKind  = "Catalog"
	catalogsPath = "/apis/firmware.liken.sh/v1alpha1/catalogs"
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
	// directory, under the name the jcat gives.
	URL string `json:"url"`
}

type catalogStatus struct {
	File       string          `json:"file,omitempty"`
	SHA256     string          `json:"sha256,omitempty"`
	ETag       string          `json:"etag,omitempty"`
	Checked    string          `json:"checked,omitempty"`
	Conditions []condition     `json:"conditions,omitempty"`
	Machines   []machineRecord `json:"machines,omitempty"`
}

// machineRecord is one machine's last scan against the catalog. The
// boot, the devices, and the file together identify one attempt, and
// each attempt runs once. The operator reads the record after a
// restart, so it holds no record of its own in memory.
type machineRecord struct {
	Machine string `json:"machine"`
	// Boot is the Machine's status.boot.time when the scan started.
	Boot string `json:"boot,omitempty"`
	// Devices is a hash of the names of the devices in the machine's
	// slice (deviceSetHash).
	Devices string `json:"devices"`
	File    string `json:"file"`
	// Releases is a hash of the catalog's releases for GUIDs, in File
	// (catalogIndex.releasesHash). Empty means the operator could not
	// compute it, and a new file then scans the machine again.
	Releases string `json:"releases,omitempty"`
	// GUIDs are the GUIDs of every updatable device the machine
	// reported in its last scan that answered.
	GUIDs   []string `json:"guids,omitempty"`
	Result  string   `json:"result"`
	Message string   `json:"message,omitempty"`
	Time    string   `json:"time"`
}

// The results of a scan record. Failed is also a result of an upload
// entry (firmware.go).
const (
	resultScanned      = "Scanned"
	resultCatalogMoved = "CatalogMoved"
)

// ready is the one condition of a Catalog: whether the operator read
// the jcat and the file it names.
const ready = "Ready"

// The reasons of Ready.
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

// reference names a Catalog in its Events.
func (c *catalog) reference() eventReference {
	return eventReference{APIVersion: firmwareAPIVersion, Kind: catalogKind, Name: c.Metadata.Name, UID: c.Metadata.UID}
}
