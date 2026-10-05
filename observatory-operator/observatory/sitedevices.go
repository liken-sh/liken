package observatory

// The devices of the site: the dome and the weather station belong to
// the Observatory, and a sky quality meter, a switch, or a receiver
// belongs to either the Observatory or one Telescope.

type (
	Dome            = Object[DomeSpec, DomeStatus]
	WeatherStation  = Object[WeatherStationSpec, WeatherStationStatus]
	SkyQualityMeter = Object[SkyQualityMeterSpec, SkyQualityMeterStatus]
	Switch          = Object[SwitchSpec, SwitchStatus]
	Receiver        = Object[ReceiverSpec, ReceiverStatus]
)

// DomeSpec describes a device with INDI's DOME_INTERFACE: a dome or a
// roll-off roof. The Observatory's policies set how it and the mounts
// lock each other.
type DomeSpec struct {
	ObservatoryDevice
}

type DomeStatus struct {
	DeviceStatus
	Readings DomeReadings `json:"readings,omitzero"`
}

// DomeReadings come from ABS_DOME_POSITION, DOME_SHUTTER, and
// DOME_PARK.
type DomeReadings struct {
	// Azimuth is in degrees, from 0 to 360, east of north.
	Azimuth *float64 `json:"azimuth,omitempty"`
	// Shutter is Open, Closed, or Moving.
	Shutter string `json:"shutter,omitempty"`
	Parked  *bool  `json:"parked,omitempty"`
}

// WeatherStationSpec describes a device with INDI's WEATHER_INTERFACE.
type WeatherStationSpec struct {
	ObservatoryDevice
}

type WeatherStationStatus struct {
	DeviceStatus
	Readings WeatherStationReadings `json:"readings,omitzero"`
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

// WeatherStationReadings come from SAFETY_STATUS, WEATHER_PARAMETERS,
// and WEATHER_STATUS.
type WeatherStationReadings struct {
	Safety     Safety             `json:"safety,omitempty"`
	Parameters []WeatherParameter `json:"parameters,omitempty"`
}

// WeatherParameter is one measurement, such as WEATHER_TEMPERATURE,
// with the verdict that the driver gives it against its limits.
type WeatherParameter struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	// Value is in the unit that the label names, such as degrees
	// Celsius or kilometers per hour.
	Value  *float64 `json:"value,omitempty"`
	Safety Safety   `json:"safety,omitempty"`
}

// SkyQualityMeterSpec describes a sky quality meter: a driver with
// INDI's AUX_INTERFACE that defines SKY_QUALITY.
type SkyQualityMeterSpec struct {
	TelescopeOrObservatoryDevice
}

type SkyQualityMeterStatus struct {
	DeviceStatus
	Readings SkyQualityMeterReadings `json:"readings,omitzero"`
}

// SkyQualityMeterReadings come from SKY_QUALITY.
type SkyQualityMeterReadings struct {
	// Brightness is the sky's brightness in magnitudes per square
	// arcsecond. A higher number is a darker sky.
	Brightness *float64 `json:"brightness,omitempty"`
}

// SwitchSpec describes a device with INDI's OUTPUT_INTERFACE or
// INPUT_INTERFACE: a relay board, or a power box's outputs. A
// device's spec.power names one of its outputs.
type SwitchSpec struct {
	TelescopeOrObservatoryDevice
}

type SwitchStatus struct {
	DeviceStatus
	Readings SwitchReadings `json:"readings,omitzero"`
}

// SwitchReadings come from the DIGITAL_OUTPUT_n and DIGITAL_INPUT_n
// properties and their labels.
type SwitchReadings struct {
	Outputs []SwitchChannel `json:"outputs,omitempty"`
	// On lists the numbers of the outputs that are on. It repeats what
	// Outputs holds, because a printer column shows only the first
	// value of a JSONPath that matches several, and `kubectl get`
	// shows this list whole.
	On     []int32         `json:"on,omitempty"`
	Inputs []SwitchChannel `json:"inputs,omitempty"`
}

// SwitchChannel is one output or one input. Number counts from 1, the
// way the driver names DIGITAL_OUTPUT_1.
type SwitchChannel struct {
	Number int32  `json:"number"`
	Label  string `json:"label,omitempty"`
	On     *bool  `json:"on,omitempty"`
}

// ReceiverSpec describes a radio receiver: a device with INDI's
// SPECTROGRAPH_INTERFACE, such as an RTL-SDR.
type ReceiverSpec struct {
	TelescopeOrObservatoryDevice
}

type ReceiverStatus struct {
	DeviceStatus
	Readings ReceiverReadings `json:"readings,omitzero"`
}

// ReceiverReadings come from RECEIVER_SETTINGS.
type ReceiverReadings struct {
	// Frequency is the center frequency in hertz.
	Frequency *float64 `json:"frequency,omitempty"`
}
