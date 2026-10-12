package observatory

// The devices on one light path, behind one optical tube.

type (
	Camera      = Object[CameraSpec, CameraStatus]
	FilterWheel = Object[FilterWheelSpec, FilterWheelStatus]
	Focuser     = Object[FocuserSpec, FocuserStatus]
	Rotator     = Object[RotatorSpec, RotatorStatus]
	DustCap     = Object[DustCapSpec, DustCapStatus]
	FlatPanel   = Object[FlatPanelSpec, FlatPanelStatus]
)

// CameraSpec describes a device with INDI's CCD_INTERFACE. The
// settings are pointers, so a setting a person leaves out differs from
// a setting of zero: the operator writes only the settings the spec
// states.
type CameraSpec struct {
	TrainDevice
	// FITSKeywords are the keywords of every frame that the camera
	// takes, the third level of MergeFITSKeywords. Unlike the settings,
	// the session records them, so an edit waits for the next session.
	FITSKeywords []FITSKeyword `json:"fitsKeywords,omitempty"`
	Procedures[CameraAction]
	CameraSettings
}

type CameraStatus struct {
	DeviceStatus
	CameraStandard
	Display CameraDisplay `json:"display,omitzero"`
}

// CameraDisplay holds the readings and the setpoint as a person reads
// them, with their units, for the printer columns.
type CameraDisplay struct {
	// Temperature is the sensor's temperature, such as -9.8 °C.
	Temperature string `json:"temperature,omitempty"`
	// Setpoint is the cool action of the camera's activation, such
	// as -10 °C.
	Setpoint string `json:"setpoint,omitempty"`
	// Cooler is On or Off, from CCD_COOLER.
	Cooler string `json:"cooler,omitempty"`
	// CoolerPower is the cooler's power, such as 42 %.
	CoolerPower string `json:"coolerPower,omitempty"`
	// Exposure is the time left, such as 12 s, while an exposure runs,
	// and the ExposureState of CCD_EXPOSURE otherwise.
	Exposure string `json:"exposure,omitempty"`
}

// ExposureState names the state of a camera's CCD_EXPOSURE in the
// display: Idle before the first exposure, Exposing while it is Busy,
// Done when it is Ok, and Failed when it is Alert.
type ExposureState string

const (
	ExposureIdle     ExposureState = "Idle"
	ExposureExposing ExposureState = "Exposing"
	ExposureDone     ExposureState = "Done"
	ExposureFailed   ExposureState = "Failed"
)

// FilterWheelSpec describes a device with INDI's FILTER_INTERFACE.
type FilterWheelSpec struct {
	TrainDevice
	Procedures[Action]
	FilterWheelSettings
}

type FilterWheelStatus struct {
	DeviceStatus
	FilterWheelStandard
	Readings FilterWheelReadings `json:"readings,omitzero"`
}

// FilterWheelReadings hold the name of the filter in the light path,
// which no typed field holds alone: FILTER_SLOT gives the slot, and
// FILTER_NAME the name in each slot.
type FilterWheelReadings struct {
	Filter string `json:"filter,omitempty"`
}

// FocuserSpec describes a device with INDI's FOCUSER_INTERFACE.
type FocuserSpec struct {
	TrainDevice
	Procedures[Action]
	FocuserSettings
}

type FocuserStatus struct {
	DeviceStatus
	FocuserStandard
	Display FocuserDisplay `json:"display,omitzero"`
}

// FocuserDisplay holds the position with its unit, such as 52000
// steps, for the printer columns.
type FocuserDisplay struct {
	Position string `json:"position,omitempty"`
}

// RotatorSpec describes a device with INDI's ROTATOR_INTERFACE.
type RotatorSpec struct {
	TrainDevice
	Procedures[Action]
	RotatorSettings
}

type RotatorStatus struct {
	DeviceStatus
	RotatorStandard
	Display RotatorDisplay `json:"display,omitzero"`
}

// RotatorDisplay holds the angle with its unit, such as 90°, for the
// printer columns.
type RotatorDisplay struct {
	Angle string `json:"angle,omitempty"`
}

// DustCapSpec describes a device with INDI's DUSTCAP_INTERFACE.
type DustCapSpec struct {
	TrainDevice
	Procedures[CoverAction]
}

type DustCapStatus struct {
	DeviceStatus
	DustCapStandard
}

// FlatPanelSpec describes a device with INDI's LIGHTBOX_INTERFACE.
type FlatPanelSpec struct {
	TrainDevice
	Procedures[LightAction]
	FlatPanelSettings
}

type FlatPanelStatus struct {
	DeviceStatus
	FlatPanelStandard
	Display FlatPanelDisplay `json:"display,omitzero"`
}

// FlatPanelDisplay holds the brightness for the printer columns. INDI
// gives FLAT_LIGHT_INTENSITY no unit, so the brightness shows against
// the driver's maximum, such as 128 of 255.
type FlatPanelDisplay struct {
	Brightness string `json:"brightness,omitempty"`
}
