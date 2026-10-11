package observatory

// The devices that belong to a Telescope as a whole: the mount that
// points it, the GPS that gives the mount its place and time, and the
// polar aligner that adjusts the mount's axis.

type (
	Mount        = Object[MountSpec, MountStatus]
	GPS          = Object[GPSSpec, GPSStatus]
	PolarAligner = Object[PolarAlignerSpec, PolarAlignerStatus]
)

// MountSpec describes a device with INDI's TELESCOPE_INTERFACE. The
// operator writes the observatory's location to the mount when it
// configures it, so the spec holds no location.
type MountSpec struct {
	TelescopeDevice
	Procedures[ParkAction]
	MountSettings
}

type MountStatus struct {
	DeviceStatus
	MountStandard
	Readings MountReadings `json:"readings,omitzero"`
	Display  MountDisplay  `json:"display,omitzero"`
}

// MountDisplay holds the coordinates as a person reads them, for the
// printer columns. RightAscension is in hours, minutes, and seconds,
// such as 19h17m21s, and Declination in degrees, arcminutes, and
// arcseconds, such as +12°34′56″.
type MountDisplay struct {
	RightAscension string `json:"rightAscension,omitempty"`
	Declination    string `json:"declination,omitempty"`
}

// MountState names what a mount does. A mount does one thing at a
// time, so one field names it, where INDI reports two switches and a
// light.
type MountState string

const (
	MountParked    MountState = "Parked"
	MountParking   MountState = "Parking"
	MountUnparking MountState = "Unparking"
	// Stopped is a mount that is unparked and does not move.
	MountStopped  MountState = "Stopped"
	MountSlewing  MountState = "Slewing"
	MountTracking MountState = "Tracking"
)

// MountReadings hold what the mount does, which no typed field holds
// alone: the operator reads it from TELESCOPE_PARK,
// TELESCOPE_TRACK_STATE, and the state of EQUATORIAL_EOD_COORD.
type MountReadings struct {
	// State is absent until the driver defines EQUATORIAL_EOD_COORD.
	State MountState `json:"state,omitempty"`
}

// GPSSpec describes a device with INDI's GPS_INTERFACE.
type GPSSpec struct {
	TelescopeDevice
	Procedures[Action]
	GPSSettings
}

type GPSStatus struct {
	DeviceStatus
	GPSStandard
}

// PolarAlignerSpec describes a device with INDI's PAC_INTERFACE: a
// motor that moves the mount's axis in altitude and azimuth.
type PolarAlignerSpec struct {
	TelescopeDevice
	Procedures[Action]
	PolarAlignerSettings
}

type PolarAlignerStatus struct {
	DeviceStatus
	PolarAlignerStandard
}
