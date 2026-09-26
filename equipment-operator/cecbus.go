package main

// The CECBus resource: one HDMI tree, which means one CEC wire, and
// the adapters on the cluster's machines that are connected to it.
// plans/09-cec.md gives the design.

// CECMode is the consent a person gives an adapter on the bus.
type CECMode string

const (
	// CECListen claims no logical address and sends nothing. The adapter
	// reports what it hears.
	CECListen CECMode = "Listen"
	// CECControl claims a logical address as a playback device, scans
	// the bus, and answers the TV.
	CECControl CECMode = "Control"
)

type CECBus struct {
	APIVersion string       `json:"apiVersion,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       CECBusSpec   `json:"spec"`
	Status     CECBusStatus `json:"status,omitempty"`
}

type CECBusList struct {
	Metadata ListMeta `json:"metadata"`
	Items    []CECBus `json:"items"`
}

type CECBusSpec struct {
	Mode     CECMode         `json:"mode,omitempty"`
	Adapters []CECBusAdapter `json:"adapters,omitempty"`
}

// CECBusAdapter names the machine that carries one adapter and the
// Display whose physical address the adapter announces.
type CECBusAdapter struct {
	Machine string `json:"machine"`
	Display string `json:"display,omitempty"`
}

// names answers whether the bus names a machine's adapter.
func (s CECBusSpec) names(machine string) (CECBusAdapter, bool) {
	for _, adapter := range s.Adapters {
		if adapter.Machine == machine {
			return adapter, true
		}
	}
	return CECBusAdapter{}, false
}

// CECBusStatus holds each adapter's own report, written by the node
// workload on that adapter's machine, and what the Deployment derives
// from all of them: the merged device list and the conditions.
type CECBusStatus struct {
	Adapters   []CECAdapterStatus `json:"adapters,omitempty"`
	Devices    []CECDevice        `json:"devices,omitempty"`
	Conditions []Condition        `json:"conditions,omitempty"`
}

// CECAdapterState is where one adapter is in its work.
type CECAdapterState string

const (
	// AdapterListening: the adapter is a monitor and reports what it
	// hears.
	AdapterListening CECAdapterState = "Listening"
	// AdapterJoining: the adapter is in Control and holds no logical
	// address yet.
	AdapterJoining CECAdapterState = "Joining"
	// AdapterJoined: the adapter holds a logical address and has not
	// finished a scan.
	AdapterJoined CECAdapterState = "Joined"
	// AdapterScanned: the adapter holds a logical address and finished
	// at least one scan.
	AdapterScanned CECAdapterState = "Scanned"
	// AdapterRefused: a call the adapter needs failed, and the message
	// gives the kernel's or the API server's text.
	AdapterRefused CECAdapterState = "Refused"
	// AdapterStopped: the node workload stopped, released the adapter's
	// logical addresses, and left the bus. The message says why.
	AdapterStopped CECAdapterState = "Stopped"
)

// CECAdapterStatus is one adapter's report.
type CECAdapterStatus struct {
	Machine         string          `json:"machine"`
	Mode            CECMode         `json:"mode,omitempty"`
	State           CECAdapterState `json:"state,omitempty"`
	Driver          string          `json:"driver,omitempty"`
	PhysicalAddress string          `json:"physicalAddress,omitempty"`
	LogicalAddress  *int            `json:"logicalAddress,omitempty"`
	OSDName         string          `json:"osdName,omitempty"`
	Devices         []CECDevice     `json:"devices,omitempty"`
	Message         string          `json:"message,omitempty"`
	// ReportedAt is when the node workload last wrote the entry. It
	// writes the entry on a steady interval even when nothing changed,
	// so an old time means the pod stopped without a word.
	ReportedAt string `json:"reportedAt,omitempty"`
}

// CECDevice is one device on the bus, as an adapter found it. A fact
// the device has not stated is absent.
type CECDevice struct {
	PhysicalAddress string `json:"physicalAddress,omitempty"`
	LogicalAddress  int    `json:"logicalAddress"`
	Type            string `json:"type,omitempty"`
	OSDName         string `json:"osdName,omitempty"`
	Vendor          string `json:"vendor,omitempty"`
	CECVersion      string `json:"cecVersion,omitempty"`
	Power           string `json:"power,omitempty"`
}
