package observatory

// The Observatory is the building and the site: the roof, the weather,
// and the location. It runs one INDI server of its own for the devices
// that no telescope owns, while at least one Reservation in it is
// active.

type Observatory = Object[ObservatorySpec, ObservatoryStatus]

type ObservatorySpec struct {
	Location Location  `json:"location"`
	Policies *Policies `json:"policies,omitempty"`
}

// Location is the site on the Earth. The operator writes it to each
// mount and GPS as GEOGRAPHIC_COORD, which takes longitude from 0 to
// 360 degrees east, and converts a negative longitude to that range.
type Location struct {
	// Latitude is in degrees north, from -90 to 90.
	Latitude float64 `json:"latitude"`
	// Longitude is in degrees east, from -180 to 180.
	Longitude float64 `json:"longitude"`
	// Elevation is in meters above mean sea level.
	Elevation float64 `json:"elevation"`
}

// Policies are the rules between the domes and the mounts. Each one is
// off unless the spec sets it. The operator writes each policy to the
// drivers, and the drivers enforce them. The shutter policies need
// nothing more, and the dome enforces them while the operator is down.
// A lock policy needs a driver to snoop the park state of a device on
// another INDI server: the dome runs on the observatory's server, and
// each mount on its telescope's. So the operator relays each park
// state across the servers, and the condition LocksRelayed reports
// the relay. While the operator is down, each driver keeps the last
// state that the operator relayed.
type Policies struct {
	// DomeLocksMount sets each mount's DOME_POLICY to DOME_LOCKS: the
	// mount does not unpark while a dome is parked. The mount does not
	// park when the dome parks: INDI leaves that to its watchdog driver.
	DomeLocksMount bool `json:"domeLocksMount,omitempty"`
	// MountLocksDome sets each dome's MOUNT_POLICY to MOUNT_LOCKS: the
	// dome does not park while a mount of any telescope in the
	// observatory is unparked.
	MountLocksDome bool `json:"mountLocksDome,omitempty"`
	// CloseShutterOnPark sets SHUTTER_CLOSE_ON_PARK in the dome's
	// DOME_SHUTTER_PARK_POLICY.
	CloseShutterOnPark bool `json:"closeShutterOnPark,omitempty"`
	// OpenShutterOnUnpark sets SHUTTER_OPEN_ON_UNPARK in the dome's
	// DOME_SHUTTER_PARK_POLICY.
	OpenShutterOnUnpark bool `json:"openShutterOnUnpark,omitempty"`
}

// ConditionLocksRelayed is True while the operator relays the park
// states that the lock policies need between the servers. An
// Observatory that sets no lock policy has no such condition.
const ConditionLocksRelayed = "LocksRelayed"

type ObservatoryStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Phase              Phase       `json:"phase,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	// Server is the observatory's own INDI server, while it runs.
	Server *Server `json:"server,omitempty"`
	// Telescopes names each Telescope whose spec.observatory names this
	// Observatory.
	Telescopes []string `json:"telescopes,omitempty"`
	// Devices lists the devices whose parent is this Observatory.
	Devices []DeviceRef `json:"devices,omitempty"`
	// Reservations names each active Reservation of a telescope here.
	Reservations []string `json:"reservations,omitempty"`
	// Weather is the verdict of the observatory's weather stations:
	// the worst one, or Unknown when no station reports.
	Weather Safety             `json:"weather,omitempty"`
	Display ObservatoryDisplay `json:"display,omitzero"`
}

// ObservatoryDisplay holds the spec's location as a person reads it,
// such as 51.4769° N and 0.0005° W, for the printer columns.
type ObservatoryDisplay struct {
	Latitude  string `json:"latitude,omitempty"`
	Longitude string `json:"longitude,omitempty"`
}

// Phase is the state in one word of a resource that runs while a
// reservation needs it: an Observatory, a Telescope, or a Guider.
type Phase string

const (
	// PhaseIdle: no reservation needs it, and nothing runs.
	PhaseIdle Phase = "Idle"
	// PhaseActivating: a reservation's activation steps run.
	PhaseActivating Phase = "Activating"
	// PhaseReady: everything it needs runs and is connected.
	PhaseReady Phase = "Ready"
	// PhaseDeactivating: a reservation's deactivation steps run.
	PhaseDeactivating Phase = "Deactivating"
	// PhaseError: a step failed. The Ready condition gives the reason.
	PhaseError Phase = "Error"
)

// Endpoint is the address of an INDI server's Service. KStars and
// astrophotography-operator connect to Host and Port.
type Endpoint struct {
	Service string `json:"service"`
	Host    string `json:"host"`
	Port    int32  `json:"port"`
}

// Server is one INDI server: its Service, and the pod that runs it.
type Server struct {
	Endpoint
	Pod  string `json:"pod,omitempty"`
	Node string `json:"node,omitempty"`
}

// DeviceRef names one device in a parent's status, with its phase.
type DeviceRef struct {
	Kind  string      `json:"kind"`
	Name  string      `json:"name"`
	Phase DevicePhase `json:"phase,omitempty"`
}
