package observatory

import (
	"encoding/json"
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
)

// A device resource describes one piece of hardware and its settings,
// and the operator starts nothing for it until a Reservation needs it.
// Each kind's spec embeds one of the structs below, which adds that
// kind's parent field to DeviceSpec. A device has at most one parent.
// A device with no parent is "not installed", the term that the
// operator's messages use, and its phase is Inventory. The operator
// creates no pod, Service, or ResourceClaim for it, because a claim
// would reserve the hardware.

// DeviceSpec holds the fields that every device kind shares. A device
// either runs its own driver (Driver) or is served by the driver of
// another resource (Device), and the CRD admits exactly one of the two.
// INDI shows every interface of one driver as one device, so a camera
// with a built-in filter wheel serves a FilterWheel through the
// camera's driver. The resource that runs the driver has the pod, the
// claim, and the power.
type DeviceSpec struct {
	Driver Driver    `json:"driver,omitzero"`
	Device *ServedBy `json:"device,omitempty"`
	Power  *Power    `json:"power,omitempty"`
	// Claim is a resource.k8s.io ResourceClaimSpec. The operator creates
	// a ResourceClaimTemplate from it, so each pod of the device gets its
	// own ResourceClaim. A simulator needs no claim. The field is raw
	// JSON so that this package does not import k8s.io/api, and the API
	// server validates it when the operator creates the template.
	Claim json.RawMessage `json:"claim,omitempty"`
}

// Driver is the INDI driver that runs the device. With no Image, the
// operator chooses the image from Name. The driver's container runs as
// user 1000 and starts Name from PATH through /usr/bin/socat, so an
// Image must contain socat and the driver and work as that user.
type Driver struct {
	Name  string `json:"name"`
	Image string `json:"image,omitempty"`
}

// ServedBy names the resource of another kind whose driver serves this
// device.
type ServedBy struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// Power is the Switch output that powers the device. Activation turns
// the output on before the device's pod starts, and deactivation turns
// it off after the pod stops.
type Power struct {
	Switch string `json:"switch"`
	Output int32  `json:"output"`
}

// Parent is the resource that a device belongs to. The zero Parent
// means that the device is not installed.
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
// a Focuser, a Rotator, a DustCap, or a FlatPanel. The train sets
// which mount, focuser, and filter wheel each camera snoops, and so
// which ones the FITS header of each frame records.
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
// level: a Switch or a Receiver. The CRD admits at most one of the two
// parent fields.
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

func parent(kind Kind, name string) Parent {
	if name == "" {
		return Parent{}
	}
	return Parent{kind, name}
}

type DevicePhase string

const (
	// DeviceInventory: the device is not installed.
	DeviceInventory DevicePhase = "Inventory"
	// DeviceIdle: the device has a parent, but no reservation of its
	// telescope or observatory is active, so it has no pod.
	DeviceIdle DevicePhase = "Idle"
	// DeviceStarting: an activation is under way or the operator is
	// creating the device's pod. The device stays Starting until the
	// pod is ready and the driver defines its properties.
	DeviceStarting DevicePhase = "Starting"
	// DeviceConnecting: the operator set CONNECTION to CONNECT and
	// waits for the driver to respond.
	DeviceConnecting DevicePhase = "Connecting"
	DeviceConnected  DevicePhase = "Connected"
	// DeviceDisconnecting: the operator set CONNECTION to DISCONNECT or
	// is deleting the device's pod.
	DeviceDisconnecting DevicePhase = "Disconnecting"
	// DeviceError: a step failed, the operator refuses the device, or
	// the driver does not define the properties of the kind's
	// interface. The Ready condition gives the reason.
	DeviceError DevicePhase = "Error"
)

// DeviceStatus holds the status fields that every device kind shares.
// Each kind's status embeds it beside the kind's generated Standard
// type, which has the standard INDI properties of that kind. Some kinds
// add a readings block, values that the operator computes from several
// properties, and a display block for the printer columns.
type DeviceStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Phase              DevicePhase `json:"phase,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	// IndiDevice is the name of the device on the INDI server, such as
	// "CCD Simulator". A device that another resource serves has the
	// name of the INDI device that serves it. It is set only while the
	// driver runs and has defined the device.
	IndiDevice string `json:"indiDevice,omitempty"`
	// Endpoint is the INDI server of the device's driver, set while the
	// driver's pod runs. A client connects to Endpoint and asks for
	// IndiDevice, so it does not need to follow spec.device.
	Endpoint *Endpoint `json:"endpoint,omitempty"`
	Driver   string    `json:"driver,omitempty"`
	// Image is the image that the device's pod runs, after the
	// operator chose one for a Driver with no image.
	Image string `json:"image,omitempty"`
	Pod   string `json:"pod,omitempty"`
	Node  string `json:"node,omitempty"`
	// Limits has the range that the driver reports for each writable
	// number of the typed fields, by property and then by member, such
	// as the least and greatest CCD_TEMPERATURE of a camera. The CRD
	// schema describes the rest of each typed field, so the status
	// copies only the range, which each driver sets for itself. The CRD
	// makes the map atomic for the reason given at Properties.
	Limits map[string]map[string]Limit `json:"limits,omitempty"`
	// Properties has every property of the INDI device that no typed
	// field covers, without BLOB data: a vendor's own properties, those
	// of INDI's DefaultDevice, and the standard properties of an
	// interface that no resource of the device represents. Only the
	// resource that runs the driver lists them, so each property is in
	// one object. The CRD makes the list and each property's members
	// atomic lists, not maps keyed by name. The operator writes the whole
	// status in one PUT, so a merge by key gains nothing, and a map would
	// make the status entry of managedFields list every property and
	// member, which is larger than the status itself.
	Properties []Property `json:"properties,omitempty"`
	// Procedures has the last run of each trigger of the device's
	// procedures.
	Procedures []ProcedureRun `json:"procedures,omitempty"`
	// SetUpUntil is when a person's SetUp expires, in place of the
	// astronomical dawn. A person sets it with SetUp, and the operator
	// keeps it.
	SetUpUntil *time.Time `json:"setUpUntil,omitempty"`
	// SetUpExpiryTime is when the SetUp condition expires: SetUpUntil,
	// or else the first astronomical dawn after SetUp became True. The
	// operator writes it while SetUp is True, for a printer column.
	SetUpExpiryTime *time.Time `json:"setUpExpiryTime,omitempty"`
}

// Limit is the range that a driver reports for one number. The driver
// refuses a value outside Min and Max. Step is zero when the driver
// reports none.
type Limit struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Step float64 `json:"step,omitempty"`
}

// Property is one INDI property as the driver defines it, with the
// values of its last update, so that a person can read a property that
// no typed field covers.
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

// Member is one element of a property. Value is its value as text: a
// number in the member's format, On or Off for a switch, a state for a
// light, or the text itself. A BLOB member has no Value, because the
// operator never requests BLOB data.
type Member struct {
	Name  string   `json:"name"`
	Label string   `json:"label,omitempty"`
	Value string   `json:"value,omitempty"`
	Min   *float64 `json:"min,omitempty"`
	Max   *float64 `json:"max,omitempty"`
	Step  *float64 `json:"step,omitempty"`
}
