package main

// The Firmware kind, as the operator reads and writes it. The struct
// holds the fields the operator reads, and every status field, because
// a write to the status subresource replaces the whole status: a
// status field missing here would be erased by each write.

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

// firmwareSpec holds exactly one of upload and update, which the CRD
// enforces. selector and machines belong to upload alone.
type firmwareSpec struct {
	Selector string                `json:"selector,omitempty"`
	Machines *metav1.LabelSelector `json:"machines,omitempty"`
	Upload   *upload               `json:"upload,omitempty"`
	Update   *update               `json:"update,omitempty"`
}

// upload names the image that holds the loader and the firmware, and
// the command that writes the firmware into one device.
type upload struct {
	Image   string   `json:"image"`
	Command []string `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// update marks a Firmware as the rule for the firmware that a device
// keeps, and its guids name the device model it covers (discovered.go).
type update struct {
	GUIDs []string `json:"guids"`
}

type firmwareStatus struct {
	ObservedGeneration int64          `json:"observedGeneration,omitempty"`
	Conditions         []condition    `json:"conditions,omitempty"`
	Devices            []deviceEntry  `json:"devices,omitempty"`
	Machines           []machineEntry `json:"machines,omitempty"`
	Counts             counts         `json:"counts"`
}

// deviceEntry is the latest appearance of a matched device in one
// port, a machine and an address, and what the operator did with it.
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

// The results of an entry.
const (
	resultRunning  = "Running"
	resultLoaded   = "Loaded"
	resultFailed   = "Failed"
	resultConflict = "Conflict"
)

// counts gives the number of upload entries in each result, and the
// number of machines that an update rule's entries name and that a
// catalog offers a release. Each count is written even when it is
// zero, so a printer column shows 0 and not an empty cell.
type counts struct {
	Running  int `json:"running"`
	Loaded   int `json:"loaded"`
	Failed   int `json:"failed"`
	Conflict int `json:"conflict"`
	Machines int `json:"machines"`
	Behind   int `json:"behind"`
}

// condition is the condition type that every liken component reports,
// with the shape of metav1.Condition.
type condition = conditions.Condition

// valid is the one condition of a Firmware: whether its selector
// compiles.
const valid = "Valid"

// The reasons of Valid.
const (
	reasonCompiled        = "Compiled"
	reasonSelectorInvalid = "SelectorInvalid"
)

func (s firmwareStatus) valid() condition {
	c, _ := conditions.Find(s.Conditions, valid)
	return c
}

// reference names a Firmware in its Events.
func (f *firmware) reference() eventReference {
	return eventReference{APIVersion: firmwareAPIVersion, Kind: firmwareKind, Name: f.Metadata.Name, UID: f.Metadata.UID}
}
