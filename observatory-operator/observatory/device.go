package observatory

import (
	"encoding/json"

	"github.com/liken-sh/liken/observatory-operator/indi"
)

// A device resource is inventory: it describes one piece of hardware
// and its settings, and the operator starts nothing for it until a
// Reservation needs it. Every device kind shares the fields below. The
// parent field differs by kind, so each kind's spec embeds one of the
// four structs below, such as TrainDevice. That struct holds the parent
// field and embeds DeviceSpec. A device has at most one parent field
// set, so it is in at most one place in the tree. A device with no
// parent is on the shelf: it is described in full, and the operator
// creates no pod, no Service, and no ResourceClaim for it, because a
// claim would reserve the hardware.

// DeviceSpec holds the fields that every device kind shares.
type DeviceSpec struct {
	Driver Driver `json:"driver"`
	Power  *Power `json:"power,omitempty"`
	// Claim is a ResourceClaimSpec, as resource.k8s.io states it. The
	// operator creates a ResourceClaim from it for the device's pod. A
	// simulator needs no claim. The field stays raw JSON, so the
	// package does not import k8s.io/api for one struct, and the API
	// server's own schema for ResourceClaim validates it when the
	// operator creates the claim.
	Claim json.RawMessage `json:"claim,omitempty"`
}

// Driver names the INDI driver that runs the device. With no Image,
// the operator resolves the image from the map that the indi build
// writes. An Image is used as written, and must hold /usr/bin/socat
// and the driver on PATH, and run as user 1000.
type Driver struct {
	Name  string `json:"name"`
	Image string `json:"image,omitempty"`
}

// Power names the Switch output that powers the device. Activation
// switches the output on before the device's pod starts, and
// deactivation switches it off after the pod stops.
type Power struct {
	Switch string `json:"switch"`
	Output int32  `json:"output"`
}

// Parent names the resource above a device in the tree. The zero
// Parent is the shelf: the device names no parent.
type Parent struct {
	Kind Kind
	Name string
}

// TelescopeDevice is a device that belongs to a Telescope: a Mount, a
// GPS, or a PolarAligner.
type TelescopeDevice struct {
	Telescope string `json:"telescope,omitempty"`
	DeviceSpec
}

func (d TelescopeDevice) Parent() Parent { return parent(TelescopeKind, d.Telescope) }

// TrainDevice is a device on one light path: a Camera, a FilterWheel,
// a Focuser, a Rotator, a DustCap, or a FlatPanel. The train's
// membership sets what each camera snoops, and so which mount,
// focuser, and filter wheel each frame's FITS header names.
type TrainDevice struct {
	OpticalTrain string `json:"opticalTrain,omitempty"`
	DeviceSpec
}

func (d TrainDevice) Parent() Parent { return parent(OpticalTrainKind, d.OpticalTrain) }

// ObservatoryDevice is a device of the site that no telescope owns: a
// Dome or a WeatherStation. It runs on the observatory's own INDI
// server.
type ObservatoryDevice struct {
	Observatory string `json:"observatory,omitempty"`
	DeviceSpec
}

func (d ObservatoryDevice) Parent() Parent { return parent(ObservatoryKind, d.Observatory) }

// TelescopeOrObservatoryDevice is a device that can belong to either
// level: a SkyQualityMeter, a Switch, or a Receiver. The CRD admits at
// most one of the two parent fields.
type TelescopeOrObservatoryDevice struct {
	Telescope   string `json:"telescope,omitempty"`
	Observatory string `json:"observatory,omitempty"`
	DeviceSpec
}

func (d TelescopeOrObservatoryDevice) Parent() Parent {
	if d.Telescope != "" {
		return Parent{TelescopeKind, d.Telescope}
	}
	return parent(ObservatoryKind, d.Observatory)
}

// parent answers the Parent that a parent field names, and the shelf
// when the field is empty.
func parent(kind Kind, name string) Parent {
	if name == "" {
		return Parent{}
	}
	return Parent{kind, name}
}

// DevicePhase is a device's state in one word.
type DevicePhase string

const (
	// DeviceInventory: the device is on the shelf. It names no parent,
	// and the operator creates nothing for it.
	DeviceInventory DevicePhase = "Inventory"
	// DeviceIdle: the device is installed, but no reservation of its
	// telescope or its observatory is active, so it has no pod.
	DeviceIdle DevicePhase = "Idle"
	// DeviceStarting: a reservation's activation began, or the
	// operator created the device's pod, and the operator waits for the
	// pod to be ready and for the driver to define its properties.
	DeviceStarting DevicePhase = "Starting"
	// DeviceConnecting: the operator set CONNECTION to CONNECT, and
	// waits for the driver to answer.
	DeviceConnecting DevicePhase = "Connecting"
	// DeviceConnected: the driver reports the device connected.
	DeviceConnected DevicePhase = "Connected"
	// DeviceDisconnecting: the operator set CONNECTION to DISCONNECT,
	// or deletes the device's pod.
	DeviceDisconnecting DevicePhase = "Disconnecting"
	// DeviceError: a step failed. The Ready condition gives the reason.
	DeviceError DevicePhase = "Error"
)

// DeviceStatus holds the status fields that every device kind shares.
// Each kind's status embeds it beside a readings block of its own.
type DeviceStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Phase              DevicePhase `json:"phase,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	// IndiDevice is the name that the driver gives its device on the
	// INDI server, such as "CCD Simulator".
	IndiDevice string `json:"indiDevice,omitempty"`
	// Driver and Image are the driver and the image that the device's
	// pod runs, after the operator resolved the image.
	Driver string `json:"driver,omitempty"`
	Image  string `json:"image,omitempty"`
	Pod    string `json:"pod,omitempty"`
	Node   string `json:"node,omitempty"`
	// Properties is every property that the device defines, as the
	// driver defines it, with no BLOB data.
	Properties []Property `json:"properties,omitempty"`
	// Procedures holds the last run of each trigger of the device's
	// procedures.
	Procedures []ProcedureRun `json:"procedures,omitempty"`
}

// Property is one INDI property, as the driver defines it and with
// the values of its last update. A person reads a vendor's property
// here, and a typed reading never depends on one.
type Property struct {
	Name       string          `json:"name"`
	Label      string          `json:"label,omitempty"`
	Group      string          `json:"group,omitempty"`
	Type       indi.Type       `json:"type"`
	Permission indi.Permission `json:"permission"`
	State      indi.State      `json:"state"`
	Rule       indi.Rule       `json:"rule,omitempty"`
	Members    []Member        `json:"members,omitempty"`
}

// Member is one element of a property. Value is the value as text: a
// number in the member's format, On or Off for a switch, a state for a
// light, or the text. A BLOB member has no value here, and the
// operator never requests BLOB data.
type Member struct {
	Name  string   `json:"name"`
	Label string   `json:"label,omitempty"`
	Value string   `json:"value,omitempty"`
	Min   *float64 `json:"min,omitempty"`
	Max   *float64 `json:"max,omitempty"`
	Step  *float64 `json:"step,omitempty"`
}
