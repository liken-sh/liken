package main

// The types hold every status field of a Firmware, not only those the
// operator reads, because a write to the status subresource replaces
// the whole status and would erase a field missing here.

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/informer"
)

const (
	firmwareAPIVersion = "firmware.liken.sh/v1alpha1"
	firmwareKind       = "Firmware"
	firmwaresPath      = "/apis/firmware.liken.sh/v1alpha1/firmwares"
)

func firmwarePath(name string) string { return firmwaresPath + "/" + name }

type firmwareMeta struct {
	Name              string            `json:"name"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Generation        int64             `json:"generation,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
}

func (m *firmwareMeta) GetName() string            { return m.Name }
func (m *firmwareMeta) GetNamespace() string       { return "" }
func (m *firmwareMeta) GetResourceVersion() string { return m.ResourceVersion }

type firmware struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   firmwareMeta   `json:"metadata"`
	Spec       firmwareSpec   `json:"spec"`
	Status     firmwareStatus `json:"status,omitzero"`
}

func (f *firmware) GetObjectMeta() informer.Meta { return &f.Metadata }

// The CRD requires exactly one of Upload and Update, and Selector only
// with Upload. Machines limits where an upload runs, or which machines
// an update's approval covers.
type firmwareSpec struct {
	Selector string                `json:"selector,omitempty"`
	Machines *metav1.LabelSelector `json:"machines,omitempty"`
	Upload   *upload               `json:"upload,omitempty"`
	Update   *update               `json:"update,omitempty"`
}

// upload is the image that holds the loader and the firmware, and the
// command that writes the firmware into one device.
type upload struct {
	Image   string   `json:"image"`
	Command []string `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// update makes a Firmware cover the installed firmware of devices that
// fwupd updates. Exactly one of Component, a catalog component, and
// GUIDs selects those devices (grouping.go). Version and Cabinet record
// a person's approval of one release (approval.go).
type update struct {
	Component string         `json:"component,omitempty"`
	GUIDs     []string       `json:"guids,omitempty"`
	Version   string         `json:"version,omitempty"`
	Cabinet   *pinnedCabinet `json:"cabinet,omitempty"`
}

// pinnedCabinet is the cabinet file, fwupd's .cab archive, that an
// approval installs. The SHA-256 pins its content, the way a digest
// pins an image.
type pinnedCabinet struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type firmwareStatus struct {
	ObservedGeneration int64          `json:"observedGeneration,omitempty"`
	Conditions         []condition    `json:"conditions,omitempty"`
	Devices            []deviceEntry  `json:"devices,omitempty"`
	Machines           []machineEntry `json:"machines,omitempty"`
	Rehearsals         []rehearsal    `json:"rehearsals,omitempty"`
	Applies            []applyRecord  `json:"applies,omitempty"`
	Counts             counts         `json:"counts,omitzero"`
}

// deviceEntry records an upload Firmware's latest appearance of a
// matched device, keyed by machine and address, and the upload's result.
type deviceEntry struct {
	Machine      string `json:"machine"`
	Device       string `json:"device"`
	Address      string `json:"address"`
	DeviceNumber int64  `json:"deviceNumber"`
	Generation   int64  `json:"generation"`
	Result       string `json:"result"`
	Message      string `json:"message,omitempty"`
	Time         string `json:"time"`
}

// The values of deviceEntry.Result.
const (
	resultRunning  = "Running"
	resultLoaded   = "Loaded"
	resultFailed   = "Failed"
	resultConflict = "Conflict"
)

// counts sets exactly one of its embedded pointers, for an upload or
// an update Firmware. encoding/json writes the embedded struct's fields
// at the level of counts, and none of them when the pointer is nil. So
// the Firmware's own printer columns show 0 when a count is zero, and
// the other kind's columns stay empty.
type counts struct {
	*UploadCounts
	*UpdateCounts
}

// UploadCounts counts an upload Firmware's device entries by result.
// It is exported because encoding/json cannot allocate an embedded
// pointer to an unexported type when it decodes.
type UploadCounts struct {
	Running  int `json:"running"`
	Loaded   int `json:"loaded"`
	Failed   int `json:"failed"`
	Conflict int `json:"conflict"`
}

// UpdateCounts counts the machines in an update Firmware's entries
// (Machines), those that a catalog offers any release (Behind), and
// those that a catalog offers the approved version in the pinned
// cabinet, among the machines that spec.machines selects (Pending).
type UpdateCounts struct {
	Machines int `json:"machines"`
	Behind   int `json:"behind"`
	Pending  int `json:"pending"`
}

type condition = conditions.Condition

// valid is the condition type that reports whether spec.selector
// compiles.
const valid = "Valid"

const (
	reasonCompiled        = "Compiled"
	reasonSelectorInvalid = "SelectorInvalid"
)

func (s firmwareStatus) valid() condition {
	c, _ := conditions.Find(s.Conditions, valid)
	return c
}

// reference returns the object reference for Events about this
// Firmware.
func (f *firmware) reference() eventReference {
	return eventReference{APIVersion: firmwareAPIVersion, Kind: firmwareKind, Name: f.Metadata.Name, UID: f.Metadata.UID}
}
