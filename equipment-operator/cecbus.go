package main

// The CECBus resource: one HDMI tree, which has one CEC wire, and the
// CEC adapters on the cluster's machines that connect to it.

// CECMode sets what the adapters of a bus may do on it.
type CECMode string

const (
	// CECListen claims no logical address and sends nothing. The adapter
	// only reports the messages it receives.
	CECListen CECMode = "Listen"
	// CECControl claims a logical address as a playback device, scans
	// the bus once when it joins, and responds to requests from the TV.
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
	Mode CECMode `json:"mode,omitempty"`
	// OSDName is the name that every adapter in Control mode announces,
	// and the TV shows it as the name of the source.
	OSDName  string          `json:"osdName,omitempty"`
	Adapters []CECBusAdapter `json:"adapters,omitempty"`
}

// defaultOSDName is the CRD's default for spec.osdName.
const defaultOSDName = "liken"

// CECBusAdapter is one adapter on the bus: the machine it is plugged
// into, and the Display whose physical address the adapter announces.
type CECBusAdapter struct {
	Machine string `json:"machine"`
	Display string `json:"display,omitempty"`
}

// names returns the entry in spec.adapters for a machine, and whether
// there is one.
func (s CECBusSpec) names(machine string) (CECBusAdapter, bool) {
	for _, adapter := range s.Adapters {
		if adapter.Machine == machine {
			return adapter, true
		}
	}
	return CECBusAdapter{}, false
}

// CECBusStatus has one entry for each adapter, which the node workload
// on that adapter's machine writes. The operator's Deployment merges
// the entries into Devices and Conditions.
type CECBusStatus struct {
	Adapters   []CECAdapterStatus `json:"adapters,omitempty"`
	Devices    []CECDevice        `json:"devices,omitempty"`
	Conditions []Condition        `json:"conditions,omitempty"`
}

// CECAdapterState is the stage that one adapter has reached.
type CECAdapterState string

const (
	// AdapterListening: the adapter is in Listen mode and reports the
	// messages it receives.
	AdapterListening CECAdapterState = "Listening"
	// AdapterJoining: the adapter is in Control mode and has no logical
	// address yet.
	AdapterJoining CECAdapterState = "Joining"
	// AdapterJoined: the adapter has a logical address and has not
	// finished a scan.
	AdapterJoined CECAdapterState = "Joined"
	// AdapterScanned: the adapter has a logical address and has finished
	// at least one scan.
	AdapterScanned CECAdapterState = "Scanned"
	// AdapterRefused: joining or listening failed, and the message is
	// the error. The node workload retries with a growing wait.
	AdapterRefused CECAdapterState = "Refused"
	// AdapterStopped: the node workload stopped, released the adapter's
	// logical addresses, and left the bus. The message gives the
	// reason.
	AdapterStopped CECAdapterState = "Stopped"
)

// CECAdapterStatus is what one adapter reports about itself.
type CECAdapterStatus struct {
	Machine         string          `json:"machine"`
	Mode            CECMode         `json:"mode,omitempty"`
	State           CECAdapterState `json:"state,omitempty"`
	Driver          string          `json:"driver,omitempty"`
	PhysicalAddress string          `json:"physicalAddress,omitempty"`
	LogicalAddress  *int            `json:"logicalAddress,omitempty"`
	OSDName         string          `json:"osdName,omitempty"`
	Devices         []CECDevice     `json:"devices,omitempty"`
	// ActiveSource is the physical address in the last Active Source
	// message that the adapter received or sent. The kernel does not
	// deliver an adapter's own transmissions back to it, so the node
	// workload records the ones it sends.
	ActiveSource string `json:"activeSource,omitempty"`
	Message      string `json:"message,omitempty"`
	// ReportedAt is when the node workload last wrote the entry. It
	// writes only when the content changes, so an old time does not mean
	// that the pod stopped. The pod's Ready condition determines whether the
	// entry is current (cecpods.go).
	ReportedAt string `json:"reportedAt,omitempty"`
}

// CECDevice is one device on the bus, as an adapter found it. A field
// is absent when the device has not reported it.
type CECDevice struct {
	PhysicalAddress string `json:"physicalAddress,omitempty"`
	LogicalAddress  int    `json:"logicalAddress"`
	Type            string `json:"type,omitempty"`
	OSDName         string `json:"osdName,omitempty"`
	Vendor          string `json:"vendor,omitempty"`
	CECVersion      string `json:"cecVersion,omitempty"`
	Power           string `json:"power,omitempty"`
	// SystemAudioMode is the audio system's last reported System Audio
	// Mode, On or Off, and SystemAudioModeAt is when the adapter learned
	// it. The scan at join reads it, and each broadcast Set System Audio
	// Mode message updates it.
	SystemAudioMode   string `json:"systemAudioMode,omitempty"`
	SystemAudioModeAt string `json:"systemAudioModeAt,omitempty"`
}
