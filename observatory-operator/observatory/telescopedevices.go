package observatory

// The devices that belong to a Telescope as a whole: the mount that
// points it, the GPS that gives the mount its location and time, and
// the polar aligner that adjusts the mount's axis.

type (
	Mount        = Object[MountSpec, MountStatus]
	GPS          = Object[GPSSpec, GPSStatus]
	PolarAligner = Object[PolarAlignerSpec, PolarAlignerStatus]
)

// MountSpec describes a device with INDI's TELESCOPE_INTERFACE. The
// spec has no location field, because the operator writes the
// Observatory's location to the mount when it configures the mount.
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

// MountDisplay holds the coordinates formatted for the printer
// columns. RightAscension is in hours, minutes, and seconds, such as
// 19h17m21s. Declination is in degrees, arcminutes, and arcseconds,
// such as +12°34′56″.
type MountDisplay struct {
	RightAscension string `json:"rightAscension,omitempty"`
	Declination    string `json:"declination,omitempty"`
}

// MountState is what the mount is doing. A mount does one thing at a
// time, so the status has one field, where INDI spreads it over the
// properties that MountReadings lists.
type MountState string

const (
	MountParked    MountState = "Parked"
	MountParking   MountState = "Parking"
	MountUnparking MountState = "Unparking"
	// Stopped is a mount that is not parked, slewing, or tracking.
	MountStopped  MountState = "Stopped"
	MountSlewing  MountState = "Slewing"
	MountTracking MountState = "Tracking"
)

// MountReadings holds values that the operator derives from several
// INDI properties: TELESCOPE_PARK, TELESCOPE_TRACK_STATE, and the
// state of EQUATORIAL_EOD_COORD.
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
