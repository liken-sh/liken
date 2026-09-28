package main

// The events this operator reads. A pod that the scheduler placed and whose
// volume the kubelet cannot mount stays Pending, and the pod's status gives
// no reason. The kubelet and the CSI driver write the reason only as a
// Warning event about the pod. The Library status copies that event's
// words, so a person reads the fault with kubectl get library.

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// The event type that reports a fault. The other type is Normal.
const eventWarning = "Warning"

// The fields this operator reads of a core/v1 Event. A kubelet writes the
// time it last saw the fault in lastTimestamp, and a component that uses the
// events.k8s.io API writes it in eventTime or series.lastObservedTime. The
// newest of the three is the time of the event.
type Event struct {
	Metadata       ObjectMeta      `json:"metadata"`
	InvolvedObject ObjectReference `json:"involvedObject"`
	Type           string          `json:"type,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Message        string          `json:"message,omitempty"`
	LastTimestamp  time.Time       `json:"lastTimestamp,omitzero"`
	EventTime      time.Time       `json:"eventTime,omitzero"`
	Series         *EventSeries    `json:"series,omitempty"`
}

// EventSeries is the count of one repeated event and the time it was last
// seen.
type EventSeries struct {
	LastObservedTime time.Time `json:"lastObservedTime,omitzero"`
}

// The collection ListWarningEvents answers.
type EventList struct {
	Items []Event `json:"items"`
}

// The newest of the times the event carries.
func (e *Event) seen() time.Time {
	seen := e.LastTimestamp
	if e.EventTime.After(seen) {
		seen = e.EventTime
	}
	if e.Series != nil && e.Series.LastObservedTime.After(seen) {
		seen = e.Series.LastObservedTime
	}
	return seen
}

func eventsPath(namespace string) string {
	return corePrefix + namespace + "/events"
}

// ListWarningEvents reads the Warning events about one named object of one
// namespace. The field selector narrows the list on the API server, so the
// read returns the events of one pod and not of the whole namespace.
func ListWarningEvents(ctx context.Context, c *Client, namespace, name string) (*EventList, error) {
	query := url.Values{"fieldSelector": {"involvedObject.name=" + name + ",type=" + eventWarning}}
	list := &EventList{}
	if err := c.RequestJSON(ctx, http.MethodGet, eventsPath(namespace)+"?"+query.Encode(), nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// The newest event of a list, or nil for an empty list.
func newestEvent(events []Event) *Event {
	var newest *Event
	for index := range events {
		event := &events[index]
		if newest == nil || event.seen().After(newest.seen()) {
			newest = event
		}
	}
	return newest
}
