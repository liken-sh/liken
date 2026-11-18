package observatory

// An Observatory is the building and the site: the roof, the weather,
// and the location. While at least one Reservation in it is active, it
// runs an INDI server of its own for the devices that no telescope
// owns.

import "time"

type Observatory = Object[ObservatorySpec, ObservatoryStatus]

type ObservatorySpec struct {
	Location Location `json:"location"`
	// FITSKeywords are the keywords of every frame taken at the site,
	// the first level of MergeFITSKeywords.
	FITSKeywords []FITSKeyword `json:"fitsKeywords,omitempty"`
	Procedures[Action]
}

// Location is the site's position on the Earth. The operator writes it
// to each mount and GPS as GEOGRAPHIC_COORD, whose longitude runs from 0
// to 360 degrees east, so the operator converts a negative longitude
// into that range.
type Location struct {
	// Latitude is in degrees north, from -90 to 90.
	Latitude float64 `json:"latitude"`
	// Longitude is in degrees east, from -180 to 180.
	Longitude float64 `json:"longitude"`
	// Elevation is in meters above mean sea level.
	Elevation float64 `json:"elevation"`
}

// ConditionLocksRelayed is True while the operator relays the park
// states between the servers for the park locks. An Observatory with no
// dome has no such condition.
const ConditionLocksRelayed = "LocksRelayed"

type ObservatoryStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Phase              Phase       `json:"phase,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	// Server is the observatory's own INDI server, while it runs.
	Server *Server `json:"server,omitempty"`
	// Telescopes lists each Telescope in this Observatory. For a
	// Telescope that a Reservation holds, the Observatory is the one the
	// Reservation recorded in status.session, so an edit of the
	// Telescope's spec.observatory takes effect when the session ends.
	Telescopes []string `json:"telescopes,omitempty"`
	// Devices lists the devices whose parent is this Observatory.
	Devices []DeviceRef `json:"devices,omitempty"`
	// Reservations lists the Reservation that holds each telescope in
	// this Observatory.
	Reservations []string `json:"reservations,omitempty"`
	// Weather is the worst safety level among the observatory's
	// weather stations, or Unknown when no station reports.
	Weather Safety             `json:"weather,omitempty"`
	Display ObservatoryDisplay `json:"display,omitzero"`
	// Procedures is the last run of each trigger of the observatory's
	// own procedures.
	Procedures []ProcedureRun `json:"procedures,omitempty"`
	// SetUpUntil is when a person's SetUp expires, instead of at the next
	// astronomical dawn. A person writes it with SetUp, and the operator
	// leaves it in place.
	SetUpUntil *time.Time `json:"setUpUntil,omitempty"`
	// SetUpExpiryTime is when the SetUp condition expires, which is
	// SetUpUntil or the next astronomical dawn. The operator writes it
	// while SetUp is True so that a printer column can show it.
	SetUpExpiryTime *time.Time `json:"setUpExpiryTime,omitempty"`
}

// ObservatoryDisplay holds the spec's location formatted for people, as
// in 51.4769° N and 0.0005° W, for the printer columns.
type ObservatoryDisplay struct {
	Latitude  string `json:"latitude,omitempty"`
	Longitude string `json:"longitude,omitempty"`
}

// Phase is the one-word state of a resource that runs while a
// reservation needs it: an Observatory, a Telescope, or a Guider.
type Phase string

const (
	// PhaseIdle means no reservation needs the resource, and nothing runs.
	PhaseIdle Phase = "Idle"
	// PhaseActivating means a reservation's activation steps are running.
	PhaseActivating Phase = "Activating"
	// PhaseReady means everything the resource needs is running and
	// connected.
	PhaseReady Phase = "Ready"
	// PhaseDeactivating means a reservation's deactivation steps are
	// running.
	PhaseDeactivating Phase = "Deactivating"
	// PhaseError means a step failed. The Ready condition gives the
	// reason.
	PhaseError Phase = "Error"
)

// Endpoint is the address of an INDI server's Service. KStars and
// astrophotography-operator connect to Host and Port.
type Endpoint struct {
	Service string `json:"service"`
	Host    string `json:"host"`
	Port    int32  `json:"port"`
}

// Server is one INDI server: its Service and the pod that runs it.
type Server struct {
	Endpoint
	Pod  string `json:"pod,omitempty"`
	Node string `json:"node,omitempty"`
}

// DeviceRef identifies one device in its parent's status, with its
// phase.
type DeviceRef struct {
	Kind  string      `json:"kind"`
	Name  string      `json:"name"`
	Phase DevicePhase `json:"phase,omitempty"`
}
