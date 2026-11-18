package events

import (
	"time"
	"unicode/utf8"
)

// ObjectReference is the object an Event is about. It is the
// involvedObject of a core/v1 Event, and `kubectl describe` finds an
// object's Events by its kind, name, and UID. An empty Namespace means
// the object is cluster-scoped.
type ObjectReference struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	UID        string `json:"uid,omitempty"`
}

// The types of an Event. Warning means a person may need to act.
// Normal means an expected transition or an action the component took.
const (
	TypeNormal  = "Normal"
	TypeWarning = "Warning"
)

// Event is a core/v1 Event as the recorder writes it. It sets no
// eventTime, because the API server requires an action and limits the
// length of reason and message only on an Event that has an eventTime.
type Event struct {
	APIVersion         string          `json:"apiVersion"`
	Kind               string          `json:"kind"`
	Metadata           Metadata        `json:"metadata"`
	InvolvedObject     ObjectReference `json:"involvedObject"`
	Reason             string          `json:"reason"`
	Message            string          `json:"message"`
	Type               string          `json:"type"`
	Source             Source          `json:"source"`
	FirstTimestamp     time.Time       `json:"firstTimestamp"`
	LastTimestamp      time.Time       `json:"lastTimestamp"`
	Count              int32           `json:"count"`
	ReportingComponent string          `json:"reportingComponent"`
	ReportingInstance  string          `json:"reportingInstance"`
}

// Metadata is the part of an Event's metadata that the recorder writes
// and reads back. The recorder creates each Event with generateName and
// keeps the name that the API server returns, so that it can patch the
// Event when it repeats.
type Metadata struct {
	Name            string `json:"name,omitempty"`
	GenerateName    string `json:"generateName,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// Source is the older form of reportingComponent and
// reportingInstance. `kubectl describe` prints it in the From column.
type Source struct {
	Component string `json:"component,omitempty"`
	Host      string `json:"host,omitempty"`
}

// The limits that the API server enforces on reason and message for an
// Event with an eventTime. The recorder's Events have no eventTime, but
// the recorder still cuts a longer reason or message to these lengths.
const (
	maxReason  = 128
	maxMessage = 1024
)

// clusterNamespace is the namespace of the Events about cluster-scoped
// objects. For an Event with no eventTime, the API server accepts such
// an Event only in default.
const clusterNamespace = "default"

func namespaceOf(object ObjectReference) string {
	if object.Namespace == "" {
		return clusterNamespace
	}
	return object.Namespace
}

// cut shortens text to at most limit bytes, at a character boundary,
// and marks a cut with an ellipsis.
func cut(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	const ellipsis = "…"
	end := limit - len(ellipsis)
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[:end] + ellipsis
}
