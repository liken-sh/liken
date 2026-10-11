package observatory

// The devices of the site: the dome and the weather station belong to
// the Observatory, and a switch or a receiver belongs to either the
// Observatory or one Telescope.

type (
	Dome           = Object[DomeSpec, DomeStatus]
	WeatherStation = Object[WeatherStationSpec, WeatherStationStatus]
	Switch         = Object[SwitchSpec, SwitchStatus]
	Receiver       = Object[ReceiverSpec, ReceiverStatus]
)

// DomeSpec describes a device with INDI's DOME_INTERFACE: a dome or a
// roll-off roof. While an Observatory has a dome, the dome and the
// mounts lock each other's park, and the shutter follows the park.
type DomeSpec struct {
	ObservatoryDevice
	Procedures[ParkAction]
	DomeSettings
}

type DomeStatus struct {
	DeviceStatus
	DomeStandard
	Display DomeDisplay `json:"display,omitzero"`
}

// DomeDisplay holds the azimuth with its unit, such as 123.4°, for the
// printer columns.
type DomeDisplay struct {
	Azimuth string `json:"azimuth,omitempty"`
}

// WeatherStationSpec describes a device with INDI's WEATHER_INTERFACE.
type WeatherStationSpec struct {
	ObservatoryDevice
	Procedures[Action]
	WeatherStationSettings
}

type WeatherStationStatus struct {
	DeviceStatus
	WeatherStationStandard
}

// Safety is a weather verdict. INDI's weather base class reports it as
// a light: Ok is Safe, Busy is Warning, Alert is Danger, and Idle is
// Unknown.
type Safety string

const (
	SafetySafe    Safety = "Safe"
	SafetyWarning Safety = "Warning"
	SafetyDanger  Safety = "Danger"
	SafetyUnknown Safety = "Unknown"
)

// SwitchSpec describes a device with INDI's OUTPUT_INTERFACE or
// INPUT_INTERFACE: a relay board, or a power box's outputs. A
// device's spec.power names one of its outputs.
type SwitchSpec struct {
	TelescopeOrObservatoryDevice
	Procedures[Action]
	SwitchSettings
}

type SwitchStatus struct {
	DeviceStatus
	SwitchStandard
	Display SwitchDisplay `json:"display,omitzero"`
}

// SwitchDisplay holds the numbers of the outputs that are on, such as
// 1, 2, 3, for the printer columns. A printer column shows only the
// first value of a JSONPath that matches several, so no column can
// list them from status.outputs.
type SwitchDisplay struct {
	On string `json:"on,omitempty"`
}

// ReceiverSpec describes a radio receiver: a device with INDI's
// SPECTROGRAPH_INTERFACE, such as an RTL-SDR.
type ReceiverSpec struct {
	TelescopeOrObservatoryDevice
	Procedures[Action]
	ReceiverSettings
}

type ReceiverStatus struct {
	DeviceStatus
	ReceiverStandard
	Display ReceiverDisplay `json:"display,omitzero"`
}

// ReceiverDisplay holds the frequency in megahertz, such as 1420.406
// MHz, for the printer columns.
type ReceiverDisplay struct {
	Frequency string `json:"frequency,omitempty"`
}
