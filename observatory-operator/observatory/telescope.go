package observatory

// A Telescope is a mount with its optics and its instruments. Each
// Telescope has its own INDI server, because INDI's snooping needs a
// mount and its trains on one server, and a server for each telescope
// limits a failure to one telescope.

type Telescope = Object[TelescopeSpec, TelescopeStatus]

type TelescopeSpec struct {
	Observatory string `json:"observatory"`
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
	// Tubes names each OpticalTube of this telescope.
	Tubes []string `json:"tubes,omitempty"`
	// Trains lists each OpticalTrain of this telescope, with its
	// devices.
	Trains []TrainRef `json:"trains,omitempty"`
	// Devices lists the devices whose parent is this Telescope.
	Devices []DeviceRef `json:"devices,omitempty"`
	// Guider is the telescope's Guider, and whether it is ready.
	Guider  *GuiderRef       `json:"guider,omitempty"`
	Display TelescopeDisplay `json:"display"`
	// Procedures holds the last run of each trigger of the telescope's
	// own procedures.
	Procedures []ProcedureRun `json:"procedures,omitempty"`
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
