package observatory

// A Telescope is a mount with its optics and its instruments. Each
// Telescope has its own INDI server. INDI's snooping needs a mount and
// its trains on one server, and a separate server for each telescope
// limits a failure to that telescope.

import "time"

type Telescope = Object[TelescopeSpec, TelescopeStatus]

type TelescopeSpec struct {
	Observatory string `json:"observatory"`
	// Horizon names the Horizon of the place where the telescope stands
	// now, in the telescope's namespace. It is empty for a telescope
	// with no horizon.
	Horizon string `json:"horizon,omitempty"`
	// FITSKeywords are the keywords for every frame that the
	// telescope's cameras take. They are the second level of
	// MergeFITSKeywords.
	FITSKeywords []FITSKeyword `json:"fitsKeywords,omitempty"`
	Procedures[Action]
}

type TelescopeStatus struct {
	ObservedGeneration int64       `json:"observedGeneration,omitempty"`
	Phase              Phase       `json:"phase,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	// Server is the telescope's INDI server, while it runs. Its
	// endpoint is what KStars and astrophotography-operator connect to.
	Server *Server `json:"server,omitempty"`
	// Reservation is the active Reservation of this telescope.
	Reservation *ReservationRef `json:"reservation,omitempty"`
	// Tubes lists the names of the OpticalTubes of this telescope.
	Tubes []string `json:"tubes,omitempty"`
	// Trains lists each OpticalTrain of this telescope, with its
	// devices.
	Trains []TrainRef `json:"trains,omitempty"`
	// Devices lists the devices whose parent is this Telescope.
	Devices []DeviceRef `json:"devices,omitempty"`
	// Guider is the telescope's Guider, and whether it is ready.
	Guider  *GuiderRef       `json:"guider,omitempty"`
	Display TelescopeDisplay `json:"display"`
	// Procedures records the last run of each trigger of the
	// telescope's own procedures.
	Procedures []ProcedureRun `json:"procedures,omitempty"`
	// SetUpUntil is a time that a person sets through SetUp. Their SetUp
	// expires then, in place of at the next astronomical dawn. The
	// operator keeps the value each time it writes the status.
	SetUpUntil *time.Time `json:"setUpUntil,omitempty"`
	// SetUpExpiryTime is when the SetUp condition expires: SetUpUntil,
	// or the next astronomical dawn. The operator writes it while SetUp
	// is True, so that a printer column can show it.
	SetUpExpiryTime *time.Time `json:"setUpExpiryTime,omitempty"`
}

// TelescopeDisplay holds what the printer columns show.
type TelescopeDisplay struct {
	// Guider is the Guider's phase, and PHD2's state while the
	// operator reads it, such as "Ready, Guiding". It is an empty
	// string for a telescope with no Guider, so the column shows an
	// empty cell, not <none>.
	Guider string `json:"guider"`
}

// ReservationRef names the reservation that holds a telescope, and
// its holder.
type ReservationRef struct {
	Name   string `json:"name"`
	Holder string `json:"holder,omitempty"`
}

// TrainRef is one OpticalTrain in a Telescope's status.
type TrainRef struct {
	Name        string      `json:"name"`
	OpticalTube string      `json:"opticalTube,omitempty"`
	Devices     []DeviceRef `json:"devices,omitempty"`
}

// GuiderRef is the Guider in a Telescope's status. Reason is the
// reason of the Guider's Ready condition, and Phase and State copy the
// Guider's own status.
type GuiderRef struct {
	Name   string      `json:"name"`
	Ready  bool        `json:"ready"`
	Reason string      `json:"reason,omitempty"`
	Phase  Phase       `json:"phase,omitempty"`
	State  GuiderState `json:"state,omitempty"`
}
