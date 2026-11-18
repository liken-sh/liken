package observatory

// An OpticalTube is a telescope's lens or mirror, and has no driver. An
// OpticalTrain is one light path: a tube, and the camera, filter wheel,
// focuser, and rotator behind it. Two trains can use one tube, as when
// an off-axis guider is a guide train on the tube of the imaging train.

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
	// Trains lists each OpticalTrain that uses this tube.
	Trains  []string           `json:"trains,omitempty"`
	Display OpticalTubeDisplay `json:"display,omitzero"`
}

// OpticalTubeDisplay holds the lengths of the spec with their unit,
// such as 80 mm, for the printer columns.
type OpticalTubeDisplay struct {
	Aperture    string `json:"aperture,omitempty"`
	FocalLength string `json:"focalLength,omitempty"`
}

type OpticalTrainSpec struct {
	Telescope   string `json:"telescope"`
	OpticalTube string `json:"opticalTube"`
}

type OpticalTrainStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	// Devices lists the devices whose spec.opticalTrain refers to this
	// train, as the session that uses each device recorded them.
	Devices []DeviceRef `json:"devices,omitempty"`
}
