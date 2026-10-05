package observatory

import "github.com/liken-sh/liken/observatory-operator/indi"

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
	// Gain is written to CCD_GAIN, in the driver's own units.
	Gain *float64 `json:"gain,omitempty"`
	// Offset is written to CCD_OFFSET, in the driver's own units.
	Offset *float64 `json:"offset,omitempty"`
	// Temperature is the cooler's setpoint in degrees Celsius,
	// written to CCD_TEMPERATURE. With no setpoint, the operator
	// leaves the cooler off.
	Temperature *float64 `json:"temperature,omitempty"`
}

type CameraStatus struct {
	DeviceStatus
	Readings CameraReadings `json:"readings,omitzero"`
}

// CameraReadings come from CCD_TEMPERATURE, CCD_COOLER_POWER, and
// CCD_EXPOSURE.
type CameraReadings struct {
	// Temperature is the sensor's temperature in degrees Celsius.
	Temperature *float64 `json:"temperature,omitempty"`
	// CoolerPower is the cooler's power in percent.
	CoolerPower *float64 `json:"coolerPower,omitempty"`
	// ExposureState is the state of CCD_EXPOSURE: Busy while an
	// exposure runs.
	ExposureState indi.State `json:"exposureState,omitempty"`
	// ExposureRemaining is the time left in the exposure, in seconds.
	ExposureRemaining *float64 `json:"exposureRemaining,omitempty"`
}

// FilterWheelSpec describes a device with INDI's FILTER_INTERFACE.
type FilterWheelSpec struct {
	TrainDevice
	// Filters names the filter in each slot, from slot 1. The
	// operator writes them to FILTER_NAME.
	Filters []string `json:"filters,omitempty"`
}

type FilterWheelStatus struct {
	DeviceStatus
	Readings FilterWheelReadings `json:"readings,omitzero"`
}

// FilterWheelReadings come from FILTER_SLOT and FILTER_NAME.
type FilterWheelReadings struct {
	// Slot counts from 1.
	Slot   *int32 `json:"slot,omitempty"`
	Filter string `json:"filter,omitempty"`
}

// FocuserSpec describes a device with INDI's FOCUSER_INTERFACE.
type FocuserSpec struct {
	TrainDevice
}

type FocuserStatus struct {
	DeviceStatus
	Readings FocuserReadings `json:"readings,omitzero"`
}

// FocuserReadings come from ABS_FOCUS_POSITION.
type FocuserReadings struct {
	// Position is in the focuser's steps.
	Position *int64 `json:"position,omitempty"`
}

// RotatorSpec describes a device with INDI's ROTATOR_INTERFACE.
type RotatorSpec struct {
	TrainDevice
}

type RotatorStatus struct {
	DeviceStatus
	Readings RotatorReadings `json:"readings,omitzero"`
}

// RotatorReadings come from ABS_ROTATOR_ANGLE.
type RotatorReadings struct {
	// Angle is in degrees, from 0 to 360.
	Angle *float64 `json:"angle,omitempty"`
}

// DustCapSpec describes a device with INDI's DUSTCAP_INTERFACE.
type DustCapSpec struct {
	TrainDevice
}

type DustCapStatus struct {
	DeviceStatus
	Readings DustCapReadings `json:"readings,omitzero"`
}

// The positions of a dust cap or a dome's shutter.
const (
	CoverOpen   = "Open"
	CoverClosed = "Closed"
	CoverMoving = "Moving"
)

// DustCapReadings come from CAP_PARK: PARK is Closed, UNPARK is Open,
// and a Busy state is Moving.
type DustCapReadings struct {
	Cover string `json:"cover,omitempty"`
}

// FlatPanelSpec describes a device with INDI's LIGHTBOX_INTERFACE.
type FlatPanelSpec struct {
	TrainDevice
}

type FlatPanelStatus struct {
	DeviceStatus
	Readings FlatPanelReadings `json:"readings,omitzero"`
}

// FlatPanelReadings come from FLAT_LIGHT_CONTROL and
// FLAT_LIGHT_INTENSITY.
type FlatPanelReadings struct {
	Light *bool `json:"light,omitempty"`
	// Brightness is in the driver's own units, from 0 to the maximum
	// that FLAT_LIGHT_INTENSITY defines.
	Brightness *float64 `json:"brightness,omitempty"`
}
