package main

import (
	"math"

	"github.com/liken-sh/liken/equipment-operator/denon"
	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/equipment-operator/wiim"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

// These types are hand-written, because generated types would pull in
// a typed client for every kind. Each type has only the fields this
// operator reads or writes, and the watches decode into the same types
// (watch.go).

const equipmentAPIVersion = "equipment.liken.sh/v1alpha1"

type ObjectMeta struct {
	Name            string            `json:"name,omitempty"`
	UID             string            `json:"uid,omitempty"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Generation      int64             `json:"generation,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}

type ListMeta struct {
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// A Receiver is an A/V receiver that a liken machine plays through. It
// is cluster-scoped because the machines connected to it belong to the
// cluster.
type Receiver struct {
	APIVersion string               `json:"apiVersion,omitempty"`
	Kind       string               `json:"kind,omitempty"`
	Metadata   ObjectMeta           `json:"metadata"`
	Spec       ReceiverSpec         `json:"spec"`
	Status     ReceiverStoredStatus `json:"status,omitempty"`
}

// session returns status.session, or spec.session when the status has
// none. media-operator writes the session into the status, because a
// status write does not change metadata.generation, and a flag change
// written to the spec would count as a spec edit. The fallback keeps an
// older media-operator that writes spec.session working, so the two
// operators can be upgraded in either order. A session that moves from
// the spec to the status compares equal, so the operator does not
// replace it.
func (r *Receiver) session() *ReceiverSession {
	if r.Status.Session != nil {
		return r.Status.Session
	}
	return r.Spec.Session
}

type ReceiverList struct {
	Metadata ListMeta   `json:"metadata"`
	Items    []Receiver `json:"items"`
}

// ReceiverSpec has exactly one protocol block, Denon or Wiim, and the
// API server refuses a Receiver with neither or both. Session is where
// an older media-operator writes the session (see Receiver.session).
type ReceiverSpec struct {
	Denon   *DenonProtocol   `json:"denon,omitempty"`
	Wiim    *WiimProtocol    `json:"wiim,omitempty"`
	Volume  *ReceiverVolume  `json:"volume,omitempty"`
	Inputs  []ReceiverInput  `json:"inputs,omitempty"`
	Session *ReceiverSession `json:"session,omitempty"`
	// Zones has the declared controls of zone2 and zone3, the only keys
	// the schema accepts. spec.power and spec.session control the main
	// zone, so a main key would give it a second writer.
	Zones map[string]ZoneSpec `json:"zones,omitempty"`
	// Power is the power state the operator sets on the receiver. The
	// operator sends each new value once and does not send it again, so
	// a person who turns the receiver on or off by hand is not
	// overruled, and a GitOps manifest that omits Power leaves the
	// receiver in the state the operator last set.
	Power equipment.Power `json:"power,omitempty"`
}

// ZoneSpec is the declared controls of zone2 or zone3. The optional
// fields are pointers, so an undeclared control differs from one set
// to zero. Volume is in the receiver's own scale, like spec.volume.max.
type ZoneSpec struct {
	Power  equipment.Power `json:"power,omitempty"`
	Input  string          `json:"input,omitempty"`
	Volume *float64        `json:"volume,omitempty"`
	Mute   *bool           `json:"mute,omitempty"`
	Sleep  *int            `json:"sleep,omitempty"`
}

// ConfirmedBy returns whether the receiver reports every declared
// control of the zone at its declared value. resolution is the number
// of the driver's volume steps in one unit of the receiver's scale. A
// control the receiver does not report is skipped, because waiting for
// it would retry forever.
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

// Pending returns the declared controls that the operator must send to
// one zone. A control that the zone reports is pending while the zone
// reports another value, so a restart sends nothing to a receiver that
// already matches, and a change made at the receiver is reversed. A
// control that the zone does not report is pending when the spec
// changed it since previous, the zone the operator last applied. known
// is false until the operator has applied zones once since it started,
// and until then no unreported control is pending.
func (z ZoneSpec) Pending(observed equipment.ZoneState, reported bool, previous ZoneSpec, known bool, resolution int) ZoneSpec {
	changed := func(same bool) bool { return known && !same }
	var pending ZoneSpec
	if z.Power != "" {
		on := z.Power != equipment.PowerStandby && z.Power != equipment.PowerOff
		if reported && observed.Power != "" {
			if on != (observed.Power == equipment.PowerOn) {
				pending.Power = z.Power
			}
		} else if changed(previous.Power == z.Power) {
			pending.Power = z.Power
		}
	}
	if z.Input != "" {
		if reported && observed.Input != "" {
			if observed.Input != z.Input {
				pending.Input = z.Input
			}
		} else if changed(previous.Input == z.Input) {
			pending.Input = z.Input
		}
	}
	if z.Volume != nil {
		steps := int(math.Round(*z.Volume * float64(resolution)))
		if reported && observed.Volume != equipment.Unknown {
			if observed.Volume != steps {
				pending.Volume = z.Volume
			}
		} else if changed(previous.Volume != nil && *previous.Volume == *z.Volume) {
			pending.Volume = z.Volume
		}
	}
	if z.Mute != nil {
		if reported {
			if observed.Mute != *z.Mute {
				pending.Mute = z.Mute
			}
		} else if changed(previous.Mute != nil && *previous.Mute == *z.Mute) {
			pending.Mute = z.Mute
		}
	}
	if z.Sleep != nil {
		if reported && observed.Sleep != equipment.Unknown {
			if observed.Sleep != *z.Sleep {
				pending.Sleep = z.Sleep
			}
		} else if changed(previous.Sleep != nil && *previous.Sleep == *z.Sleep) {
			pending.Sleep = z.Sleep
		}
	}
	return pending
}

// ReceiverVolume is read by media-operator, which turns volume key
// presses into status.session.volumeAsk. This operator reads none of
// it. Max and Step are in the receiver's own scale, and an absent Step
// is one whole unit.
type ReceiverVolume struct {
	Max       float64 `json:"max,omitempty"`
	Step      float64 `json:"step,omitempty"`
	Indicator string  `json:"indicator,omitempty"`
}

// DenonProtocol selects the Denon and Marantz control protocol.
type DenonProtocol struct {
	Address  string         `json:"address"`
	Settings denon.Settings `json:"settings,omitempty"`
}

// WiimProtocol identifies a WiiM by its LinkPlay UUID. Address is
// optional, because network discovery finds the device's current
// address from the UUID. When Address is declared, the driver still
// reads the device's UUID and sends nothing to a device whose UUID
// differs. With network discovery off (main.go) and no declared
// address, the driver has no address and reports the receiver
// unreachable. Other brands build on the LinkPlay platform and answer
// the same API, so the driver sends nothing to a device whose
// getStatusEx project does not begin with WiiM (wiim.IsWiiM), and
// discovery creates no Receiver for a device whose UPnP model does not
// (discovery_description.go).
type WiimProtocol struct {
	UUID     string        `json:"uuid"`
	Address  string        `json:"address,omitempty"`
	Settings wiim.Settings `json:"settings,omitempty"`
}

// ReceiverInput is one input of the receiver, with the machine and
// monitor that feed it. SoundMode, when set, is the sound mode the
// operator selects with this input, so a Play changes the picture and
// the mode together.
type ReceiverInput struct {
	Name      string `json:"name"`
	Machine   string `json:"machine"`
	Monitor   string `json:"monitor"`
	SoundMode string `json:"soundMode,omitempty"`
}

// ReceiverSession is the Player that plays through the receiver and the
// input it uses. media-operator keeps a session while the Player has a
// screen, including the idle screen. Active is true while a Play
// exists, and Awake is true while the room's screen is awake. When both
// are false, the operator sends the receiver no power or input command
// for them. VolumeAsk, PowerAsk, and InputAsk are requests that
// media-operator writes for key presses, and the operator applies each
// one once, when its At changes (session_asks.go).
type ReceiverSession struct {
	Player    string             `json:"player"`
	Input     string             `json:"input"`
	Active    bool               `json:"active,omitempty"`
	Awake     bool               `json:"awake,omitempty"`
	VolumeAsk *ReceiverVolumeAsk `json:"volumeAsk,omitempty"`
	PowerAsk  *ReceiverPowerAsk  `json:"powerAsk,omitempty"`
	InputAsk  *ReceiverInputAsk  `json:"inputAsk,omitempty"`
}

// ReceiverVolumeAsk requests an absolute volume and mute. Level is in
// the receiver's own scale, like spec.volume.max.
type ReceiverVolumeAsk struct {
	Level float64 `json:"level"`
	Mute  bool    `json:"mute,omitempty"`
	At    string  `json:"at"`
}

// ReceiverPowerAsk has the Action toggle, on, or off, which
// session_power.go applies to the room.
type ReceiverPowerAsk struct {
	Action string `json:"action"`
	At     string `json:"at"`
}

// ReceiverInputAsk requests the session's input. media-operator writes
// the Action ensure for any key press, and show for a home press. show
// also asks the TV to show the session's Display (session_ensure.go).
type ReceiverInputAsk struct {
	Action string `json:"action"`
	At     string `json:"at"`
}

// withoutFlags returns the session without Active, Awake, and the
// requests. setSession compares sessions with it: a change of only
// those fields updates the running session, and any other change
// replaces it.
func (s ReceiverSession) withoutFlags() ReceiverSession {
	s.Active = false
	s.Awake = false
	s.VolumeAsk, s.PowerAsk, s.InputAsk = nil, nil, nil
	return s
}

// ReceiverStatus is the part of the status that this operator applies.
type ReceiverStatus struct {
	// Address is the address the operator reached the receiver on. It
	// is the resolved address for a receiver declared by name, and the
	// discovered address for a WiiM.
	Address string `json:"address,omitempty"`
	// Model and Manufacturer are empty until the driver reads them, and
	// always empty for a driver that cannot read them.
	Model        string                `json:"model,omitempty"`
	Manufacturer string                `json:"manufacturer,omitempty"`
	Zones        map[string]ZoneStatus `json:"zones,omitempty"`
	Driver       string                `json:"driver,omitempty"`
	Denon        *denon.Settings       `json:"denon,omitempty"`
	Wiim         *wiim.Status          `json:"wiim,omitempty"`
	// SettledSettings holds a digest of each declared block the operator
	// has sent, including the fields the receiver does not report, keyed
	// by the block's path in the spec. After a restart the operator
	// sends a block's unreported fields again only when the block's
	// digest differs from the stored one. The digest covers only the
	// block, so a session flag or a label change does not alter it.
	SettledSettings map[string]string `json:"settledSettings,omitempty"`
	// SettledPower is the last spec.power that the operator applied: it
	// sent the value, found the receiver already at it, or adopted it at
	// startup. After a restart, the operator sends nothing when
	// spec.power equals it.
	SettledPower equipment.Power `json:"settledPower,omitempty"`
	Conditions   []Condition     `json:"conditions,omitempty"`
}

// ReceiverStoredStatus is the status as the API server stores it. Each
// writer uses its own field manager: the operator applies a
// ReceiverStatus, which has no Session field, so the apply neither sets
// nor removes the session. media-operator writes Session, and the
// Deployment's CECBus loop writes CEC.
//
// An earlier version of the operator wrote SettingsGeneration and
// PowerGeneration. The operator writes neither, so its first status
// apply removes them. A status with SettingsGeneration and no
// SettledSettings came from that version, which had already sent the
// declared settings, so the operator records each declared block as
// settled (newSettledRecord).
type ReceiverStoredStatus struct {
	ReceiverStatus
	Session *ReceiverSession `json:"session,omitempty"`
	// CEC describes the receiver as the audio system of a CECBus
	// (receiver_cec.go).
	CEC                *ReceiverCECStatus `json:"cec,omitempty"`
	SettingsGeneration int64              `json:"settingsGeneration,omitempty"`
	PowerGeneration    int64              `json:"powerGeneration,omitempty"`
}

// ZoneStatus is one zone in the receiver's own units. Volume and
// VolumeMax are the driver's values as strings, formatted for a person
// to read.
type ZoneStatus struct {
	Power     string `json:"power,omitempty"`
	Input     string `json:"input,omitempty"`
	SoundMode string `json:"soundMode,omitempty"`
	Mute      bool   `json:"mute,omitempty"`
	Volume    string `json:"volume,omitempty"`
	VolumeMax string `json:"volumeMax,omitempty"`
	Sleep     int    `json:"sleep,omitempty"`
}

// ConditionStatus is the driver contract's own type, so a driver's
// reachability result is written to the status without conversion.
type ConditionStatus = equipment.ConditionStatus

const (
	ConditionTrue    = equipment.ConditionTrue
	ConditionFalse   = equipment.ConditionFalse
	ConditionUnknown = equipment.ConditionUnknown
)

// Condition is the condition type that every liken component shares,
// in the shape of metav1.Condition.
type Condition = conditions.Condition
