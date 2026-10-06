package observatory

import "time"

// A Guider runs PHD2 for one Telescope, as a client of the telescope's
// INDI server. It belongs to the Telescope, because guiding corrects
// where the mount points. While a Reservation holds the telescope, the
// operator runs PHD2, connects it to the guide camera and the mount,
// and leaves it idle: the holder aligns the mount, then calibrates and
// guides through PHD2's event server. The operator reads PHD2's state
// back from that server into the status.

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

// GuiderState is PHD2's AppState, as its event server names it.
type GuiderState string

const (
	GuiderStopped     GuiderState = "Stopped"
	GuiderSelected    GuiderState = "Selected"
	GuiderCalibrating GuiderState = "Calibrating"
	GuiderGuiding     GuiderState = "Guiding"
	GuiderLostLock    GuiderState = "LostLock"
	GuiderPaused      GuiderState = "Paused"
	GuiderLooping     GuiderState = "Looping"
)

type GuiderStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Phase              Phase       `json:"phase,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	// Endpoint is PHD2's event server, while the guider's pod exists.
	// KStars and astrophotography-operator drive PHD2 there.
	Endpoint *Endpoint `json:"endpoint,omitempty"`
	// Pod and Node name the guider's pod and where it runs.
	Pod  string `json:"pod,omitempty"`
	Node string `json:"node,omitempty"`
	// State is PHD2's state, while the operator's connection to its
	// event server is open.
	State GuiderState `json:"state,omitempty"`
	// Calibrated is true while PHD2 holds a calibration of the mount.
	Calibrated *bool `json:"calibrated,omitempty"`
	// PixelScale is the guide camera's scale in arc-seconds per pixel.
	PixelScale *float64 `json:"pixelScale,omitempty"`
	// RMS is the spread of the last guide steps, in arc-seconds.
	RMS *GuiderRMS `json:"rms,omitempty"`
	// Star is the guide star of the last guide step.
	Star *GuideStar `json:"star,omitempty"`
	// LastStepTime is when PHD2 sent the last guide step.
	LastStepTime *time.Time `json:"lastStepTime,omitempty"`
	// Alert is the last alert that PHD2 showed.
	Alert   *GuiderAlert  `json:"alert,omitempty"`
	Display GuiderDisplay `json:"display,omitzero"`
}

// GuiderRMS is the root mean square of the star's distance from the
// lock position over the last guide steps, along the mount's axes and
// in total, in arc-seconds.
type GuiderRMS struct {
	RA    float64 `json:"ra"`
	Dec   float64 `json:"dec"`
	Total float64 `json:"total"`
	// Steps counts the guide steps that the RMS covers, at most 100.
	Steps int32 `json:"steps"`
}

// GuideStar is the guide star as PHD2 measured it.
type GuideStar struct {
	SNR float64 `json:"snr"`
	// HFD is the star's half-flux diameter in pixels.
	HFD float64 `json:"hfd"`
}

// GuiderAlert is one alert from PHD2.
type GuiderAlert struct {
	Message string `json:"message"`
	// Type is info, question, warning, or error.
	Type string    `json:"type"`
	Time time.Time `json:"time"`
}

// GuiderDisplay holds what the printer columns show.
type GuiderDisplay struct {
	// RMS is the total RMS with its unit, such as 0.84 arcsec.
	RMS string `json:"rms,omitempty"`
}
