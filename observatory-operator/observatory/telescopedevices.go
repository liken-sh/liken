package observatory

import (
	"time"

	"github.com/liken-sh/liken/observatory-operator/indi"
)

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
}

type MountStatus struct {
	DeviceStatus
	Readings MountReadings `json:"readings,omitzero"`
	Display  MountDisplay  `json:"display,omitzero"`
}

// MountDisplay holds the readings as a person reads them, for the
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

// MountReadings come from EQUATORIAL_EOD_COORD, TELESCOPE_PARK, and
// TELESCOPE_TRACK_STATE. A nil field is a reading the driver has not
// sent.
type MountReadings struct {
	// RightAscension is in hours, from 0 to 24, in the equinox of the
	// date (JNow), as INDI sends it.
	RightAscension *float64 `json:"rightAscension,omitempty"`
	// Declination is in degrees, from -90 to 90, in the equinox of the
	// date (JNow).
	Declination *float64 `json:"declination,omitempty"`
	// State is absent until the driver defines TELESCOPE_PARK,
	// TELESCOPE_TRACK_STATE, and EQUATORIAL_EOD_COORD.
	State MountState `json:"state,omitempty"`
}

// GPSSpec describes a device with INDI's GPS_INTERFACE.
type GPSSpec struct {
	TelescopeDevice
	Procedures[Action]
}

type GPSStatus struct {
	DeviceStatus
	Readings GPSReadings `json:"readings,omitzero"`
}

// GPSReadings come from GEOGRAPHIC_COORD and TIME_UTC. Fix is True
// when GEOGRAPHIC_COORD is in the Ok state, which is how INDI's GPS
// base class reports a fix.
type GPSReadings struct {
	Fix  *bool      `json:"fix,omitempty"`
	Time *time.Time `json:"time,omitempty"`
	// Latitude is in degrees north, from -90 to 90.
	Latitude *float64 `json:"latitude,omitempty"`
	// Longitude is in degrees east, from -180 to 180.
	Longitude *float64 `json:"longitude,omitempty"`
	// Elevation is in meters above mean sea level.
	Elevation *float64 `json:"elevation,omitempty"`
}

// PolarAlignerSpec describes a device with INDI's PAC_INTERFACE: a
// motor that moves the mount's axis in altitude and azimuth.
type PolarAlignerSpec struct {
	TelescopeDevice
	Procedures[Action]
}

type PolarAlignerStatus struct {
	DeviceStatus
	Readings PolarAlignerReadings `json:"readings,omitzero"`
}

// PolarAlignerReadings hold the state of PAC_MANUAL_ADJUSTMENT: Busy
// while the motors move, Ok when a move ended, Alert when it failed.
type PolarAlignerReadings struct {
	Adjustment indi.State `json:"adjustment,omitempty"`
}
