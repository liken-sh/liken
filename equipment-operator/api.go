package main

import (
	"math"

	"github.com/liken-sh/equipment-operator/denon"
	"github.com/liken-sh/equipment-operator/equipment"
	"github.com/liken-sh/equipment-operator/wiim"
)

// The wire types are hand-written, the way liken and the sibling
// operators write theirs. The Kubernetes API is HTTPS that serves
// JSON, and importing client-go for a dozen structs brings informers,
// work queues, and a release cadence this program does not use. Each
// type carries only the fields this operator reads or writes; the
// API server fills in the rest.

// The group this operator serves.
const equipmentAPIVersion = "equipment.liken.sh/v1alpha1"

// ObjectMeta carries what this operator reads or writes: name for the
// URL, and resourceVersion for the conditional write.
type ObjectMeta struct {
	Name            string            `json:"name,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Generation      int64             `json:"generation,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}

// A list's own resourceVersion is the revision of the whole
// collection, which is what a watch resumes from.
type ListMeta struct {
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// A Receiver describes one piece of A/V equipment on the far end of a
// machine's cable. It is cluster-scoped because the machines that feed
// it belong to the cluster.
type Receiver struct {
	APIVersion string         `json:"apiVersion,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Metadata   ObjectMeta     `json:"metadata"`
	Spec       ReceiverSpec   `json:"spec"`
	Status     ReceiverStatus `json:"status,omitempty"`
}

type ReceiverList struct {
	Metadata ListMeta   `json:"metadata"`
	Items    []Receiver `json:"items"`
}

// The spec names one protocol block, the inputs that liken machines
// feed, the non-main zones, the session that currently uses the
// receiver, and the bus topics that configure it.
type ReceiverSpec struct {
	Denon   *DenonProtocol   `json:"denon,omitempty"`
	Wiim    *WiimProtocol    `json:"wiim,omitempty"`
	Volume  *ReceiverVolume  `json:"volume,omitempty"`
	Inputs  []ReceiverInput  `json:"inputs,omitempty"`
	Session *ReceiverSession `json:"session,omitempty"`
	// Zones holds the non-main zones' declared controls. The main zone
	// is spec.power and spec.session, so a zones map may hold only
	// zone2 and zone3; a main key would give one zone two writers and
	// is rejected.
	Zones map[string]ZoneSpec `json:"zones,omitempty"`
	// SettingsTopic and CommandsTopic are the full MQTT topics the bus
	// configures this receiver on. A topic that is empty subscribes the
	// unit to nothing.
	SettingsTopic string `json:"settingsTopic,omitempty"`
	CommandsTopic string `json:"commandsTopic,omitempty"`
	// Power is the power state the operator should drive the receiver
	// to. It is the operator's own field, applied once per change and
	// never re-asserted, so a GitOps manifest that omits it leaves the
	// receiver wherever the operator last put it.
	Power equipment.Power `json:"power,omitempty"`
}

// ZoneSpec is one non-main zone's declared controls. Every optional
// field is a pointer, so a control the owner does not declare differs
// from one set to zero. Volume is in display units, the way
// spec.volume.max is.
type ZoneSpec struct {
	Power  equipment.Power `json:"power,omitempty"`
	Input  string          `json:"input,omitempty"`
	Volume *float64        `json:"volume,omitempty"`
	Mute   *bool           `json:"mute,omitempty"`
	Sleep  *int            `json:"sleep,omitempty"`
}

// ConfirmedBy answers whether the receiver has reported one declared
// zone control at the declared value. Volume is compared in the
// driver's smallest steps, so resolution turns the declared display
// units into a count before it is compared. A control the receiver has
// not reported is skipped: there is nothing to confirm it against, and
// blocking on it would retry forever. A control reported at another
// value is not confirmed, so the operator sends it again.
func (z ZoneSpec) ConfirmedBy(observed equipment.ZoneState, resolution int) bool {
	if z.Power != "" && observed.Power != "" {
		on := z.Power != equipment.PowerStandby && z.Power != equipment.PowerOff
		if on != (observed.Power == equipment.PowerOn) {
			return false
		}
	}
	if z.Input != "" && observed.Input != "" && z.Input != observed.Input {
		return false
	}
	if z.Volume != nil && observed.Volume != equipment.Unknown {
		steps := int(math.Round(*z.Volume * float64(resolution)))
		if steps != observed.Volume {
			return false
		}
	}
	if z.Mute != nil && *z.Mute != observed.Mute {
		return false
	}
	if z.Sleep != nil && observed.Sleep != equipment.Unknown && *z.Sleep != observed.Sleep {
		return false
	}
	return true
}

// How loud the room may get and how far one press moves it, both in the
// receiver's own scale. A Denon requires max. An absent step is one
// whole unit of that scale.
type ReceiverVolume struct {
	Max  float64 `json:"max,omitempty"`
	Step float64 `json:"step,omitempty"`
}

// The Denon and Marantz control protocol, the address it answers on,
// and the settings the operator drives it to.
type DenonProtocol struct {
	Address  string         `json:"address"`
	Settings denon.Settings `json:"settings,omitempty"`
}

// The WiiM control protocol. The LinkPlay UUID is the device's identity
// and the only key the operator trusts: a declared address is a hint,
// and the driver reads the device's own uuid and compares it before it
// drives anything. The address is optional because a later discovery
// path resolves the UUID to a current address; with no address and no
// discovery yet, the driver can reach nothing and reports the receiver
// unreachable.
type WiimProtocol struct {
	UUID     string        `json:"uuid"`
	Address  string        `json:"address,omitempty"`
	Settings wiim.Settings `json:"settings,omitempty"`
}

// One input of the receiver, and the machine and monitor id that feed
// it. SoundMode, when present, is the mode the receiver selects with
// this input, so a Play brings the picture and the mode together.
type ReceiverInput struct {
	Name      string `json:"name"`
	Machine   string `json:"machine"`
	Monitor   string `json:"monitor"`
	SoundMode string `json:"soundMode,omitempty"`
}

// A session names the Player, the input it uses, and the topic from
// which it reads the level. The operator derives the owner-mark topic
// by adding /owner to VolumeTopic.
// Active says that a Play is present. Awake says that the room's screen
// is awake. The media operator keeps a session while the Player has a
// screen, including the idle screen. A session with both flags false
// owns the level but sends no power or input command to the receiver.
type ReceiverSession struct {
	Player      string `json:"player"`
	Input       string `json:"input"`
	VolumeTopic string `json:"volumeTopic"`
	PowerTopic  string `json:"powerTopic,omitempty"`
	Active      bool   `json:"active,omitempty"`
	Awake       bool   `json:"awake,omitempty"`
}

// withoutFlags removes Active and Awake before comparing sessions. A
// change to either flag updates the existing session. Any other change
// replaces the session.
func (s ReceiverSession) withoutFlags() ReceiverSession {
	s.Active = false
	s.Awake = false
	return s
}

// What the receiver last reported, grouped by zone, the protocol's own
// typed settings snapshot, the Service that represents the receiver
// when one exists, and the Reachable condition.
type ReceiverStatus struct {
	Zones      map[string]ZoneStatus `json:"zones,omitempty"`
	Driver     string                `json:"driver,omitempty"`
	Denon      *denon.Settings       `json:"denon,omitempty"`
	Wiim       *wiim.Status          `json:"wiim,omitempty"`
	Service    string                `json:"service,omitempty"`
	Conditions []Condition           `json:"conditions,omitempty"`
}

// ZoneStatus is one zone in the receiver's own units. Volume and
// VolumeMax are the counts the driver reports, written the way a person
// reads them.
type ZoneStatus struct {
	Power     string `json:"power,omitempty"`
	Input     string `json:"input,omitempty"`
	SoundMode string `json:"soundMode,omitempty"`
	Mute      bool   `json:"mute,omitempty"`
	Volume    string `json:"volume,omitempty"`
	VolumeMax string `json:"volumeMax,omitempty"`
	Sleep     int    `json:"sleep,omitempty"`
}

// ConditionStatus is the three-valued verdict a condition carries. It
// is the driver contract's own type, so a reachability verdict travels
// from a driver to the status without a conversion.
type ConditionStatus = equipment.ConditionStatus

const (
	ConditionTrue    = equipment.ConditionTrue
	ConditionFalse   = equipment.ConditionFalse
	ConditionUnknown = equipment.ConditionUnknown
)

// Condition mirrors metav1.Condition, the shape Kubernetes uses
// everywhere, and liken's own. Anyone who reads kubectl describe
// output on a Pod already knows how to read one of these.
//
// ObservedGeneration records which metadata.generation the condition
// judged. Generation counts spec edits, so a reader can tell "Reachable,
// for the spec as it stands" from "Reachable, but for a spec two edits
// ago".
type Condition struct {
	Type               string          `json:"type"`
	Status             ConditionStatus `json:"status"`
	ObservedGeneration int64           `json:"observedGeneration,omitempty"`
	Reason             string          `json:"reason,omitempty"`
	Message            string          `json:"message,omitempty"`
	LastTransitionTime string          `json:"lastTransitionTime"`
}
