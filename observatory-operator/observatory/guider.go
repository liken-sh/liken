package observatory

// A Guider runs PHD2 for one Telescope, as a client of the telescope's
// INDI server. It belongs to the Telescope, because guiding corrects
// where the mount points. Plan 09 builds the guider's pod. Until then
// the operator reports the Guider's Ready condition as False with the
// reason NotImplemented.

type Guider = Object[GuiderSpec, GuiderStatus]

// PulseTarget is where PHD2 sends its guide pulses.
type PulseTarget string

const (
	// PulsesMount sends the pulses to the mount's driver.
	PulsesMount PulseTarget = "Mount"
	// PulsesCamera sends the pulses through the guide camera's ST-4
	// port.
	PulsesCamera PulseTarget = "Camera"
)

type GuiderSpec struct {
	Telescope string `json:"telescope"`
	// OpticalTrain names the train whose camera guides.
	OpticalTrain string      `json:"opticalTrain"`
	Pulses       PulseTarget `json:"pulses"`
}

type GuiderStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Phase              Phase       `json:"phase,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
}

// ReasonNotImplemented is the reason of the Guider's Ready condition
// until plan 09 builds the guider's pod.
const ReasonNotImplemented = "NotImplemented"
