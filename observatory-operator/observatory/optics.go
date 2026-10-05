package observatory

// The optics of a telescope. An OpticalTube collects the light and has
// no driver. An OpticalTrain is one light path: a tube, and the camera,
// filter wheel, focuser, and rotator behind it. Two trains can name
// one tube: an off-axis guider is a guide train on the imaging train's
// tube.

type (
	OpticalTube  = Object[OpticalTubeSpec, OpticalTubeStatus]
	OpticalTrain = Object[OpticalTrainSpec, OpticalTrainStatus]
)

type OpticalTubeSpec struct {
	Telescope string `json:"telescope"`
	// Aperture is the diameter of the lens or the primary mirror, in
	// millimeters.
	Aperture float64 `json:"aperture"`
	// FocalLength is in millimeters, with any reducer or barlow that
	// stays on the tube.
	FocalLength float64 `json:"focalLength"`
}

type OpticalTubeStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	// Trains names each OpticalTrain that uses this tube.
	Trains []string `json:"trains,omitempty"`
}

type OpticalTrainSpec struct {
	Telescope   string `json:"telescope"`
	OpticalTube string `json:"opticalTube"`
}

type OpticalTrainStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	// Devices lists the devices whose spec.opticalTrain names this
	// train.
	Devices []DeviceRef `json:"devices,omitempty"`
}
