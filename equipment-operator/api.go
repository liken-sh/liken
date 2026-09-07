package main

// The wire types are hand-written, the way liken and the sibling
// operators write theirs. The Kubernetes API is HTTPS that serves
// JSON, and importing client-go for a dozen structs brings informers,
// work queues, and a release cadence this program does not use. Each
// type carries only the fields this operator reads or writes; the
// API server fills in the rest.

// The group this operator serves.
const equipmentAPIVersion = "equipment.liken.sh/v1alpha1"

// ObjectMeta carries what this operator reads or writes: name for the
// URL, and resourceVersion for the conditional write.
type ObjectMeta struct {
	Name            string `json:"name,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Generation      int64  `json:"generation,omitempty"`
}

// A list's own resourceVersion is the revision of the whole
// collection, which is what a watch resumes from.
type ListMeta struct {
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// A Receiver is one piece of A/V equipment on the far end of a
// machine's cable. It is cluster-scoped, because the machines that feed
// it are the cluster's.
type Receiver struct {
	APIVersion string         `json:"apiVersion,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Metadata   ObjectMeta     `json:"metadata"`
	Spec       ReceiverSpec   `json:"spec"`
	Status     ReceiverStatus `json:"status,omitempty"`
}

type ReceiverList struct {
	Metadata ListMeta   `json:"metadata"`
	Items    []Receiver `json:"items"`
}

// The spec names one protocol block, the inputs liken machines feed,
// and the session that holds the receiver now.
type ReceiverSpec struct {
	Denon   *DenonProtocol   `json:"denon,omitempty"`
	Inputs  []ReceiverInput  `json:"inputs,omitempty"`
	Session *ReceiverSession `json:"session,omitempty"`
}

// The Denon and Marantz control protocol, and the address it answers
// on.
type DenonProtocol struct {
	Address string `json:"address"`
}

// One input of the receiver, and the machine and monitor id that feed
// it.
type ReceiverInput struct {
	Name    string `json:"name"`
	Machine string `json:"machine"`
	Monitor string `json:"monitor"`
}

// The Player that holds the receiver, the input it plays through, and
// the topic it takes the level from.
type ReceiverSession struct {
	Player      string `json:"player"`
	Input       string `json:"input"`
	VolumeTopic string `json:"volumeTopic"`
}

// What the receiver last said, in its own units, and the Reachable
// condition.
type ReceiverStatus struct {
	Power      string      `json:"power,omitempty"`
	Input      string      `json:"input,omitempty"`
	Volume     string      `json:"volume,omitempty"`
	VolumeMax  string      `json:"volumeMax,omitempty"`
	Mute       bool        `json:"mute,omitempty"`
	SoundMode  string      `json:"soundMode,omitempty"`
	Service    string      `json:"service,omitempty"`
	Conditions []Condition `json:"conditions,omitempty"`
}

// ConditionStatus is the three-valued verdict a condition carries.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "True"
	ConditionFalse   ConditionStatus = "False"
	ConditionUnknown ConditionStatus = "Unknown"
)

// Condition mirrors metav1.Condition, the shape Kubernetes uses
// everywhere, and liken's own. Anyone who reads kubectl describe
// output on a Pod already knows how to read one of these.
//
// ObservedGeneration records which metadata.generation the condition
// judged. Generation counts spec edits, so a reader can tell "Reachable,
// for the spec as it stands" from "Reachable, but for a spec two edits
// ago".
type Condition struct {
	Type               string          `json:"type"`
	Status             ConditionStatus `json:"status"`
	ObservedGeneration int64           `json:"observedGeneration,omitempty"`
	Reason             string          `json:"reason,omitempty"`
	Message            string          `json:"message,omitempty"`
	LastTransitionTime string          `json:"lastTransitionTime"`
}
