package main

// This file writes the record a capture leaves on the cluster. Every
// request that produces bytes writes a Kubernetes Event on the Player,
// so `kubectl describe player` answers who looked and when without a
// log search. The Event names the subject and the aspect and nothing
// else: the request log line is the detail record, and neither it nor
// the Event ever carries the token or any hash of it, because an
// Event is readable by anyone with get on events in the namespace.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// The reason and the type `kubectl describe` prints, the component
// the Event names as its source, and the collection's path segment.
const (
	capturedReason    = "Captured"
	normalEventType   = "Normal"
	eventSourceAPI    = "media-api"
	eventsPathSegment = "/events"
)

// Event is the core v1 Event as this client writes one: the fields
// `kubectl describe` prints and the two timestamps a Count of one
// needs.
type Event struct {
	APIVersion     string          `json:"apiVersion,omitempty"`
	Kind           string          `json:"kind,omitempty"`
	Metadata       ObjectMeta      `json:"metadata"`
	InvolvedObject ObjectReference `json:"involvedObject"`
	Reason         string          `json:"reason,omitempty"`
	Message        string          `json:"message,omitempty"`
	Type           string          `json:"type,omitempty"`
	Source         EventSource     `json:"source,omitempty"`
	FirstTimestamp string          `json:"firstTimestamp,omitempty"`
	LastTimestamp  string          `json:"lastTimestamp,omitempty"`
	Count          int             `json:"count,omitempty"`
}

type ObjectReference struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Name       string `json:"name,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	UID        string `json:"uid,omitempty"`
}

type EventSource struct {
	Component string `json:"component,omitempty"`
}

// recordCapture writes the Captured Event for one request. The Event
// name is the Player's name plus the request id, so each capture is
// its own Event and the id ties it to the log line. A failure to
// write it is a line on stderr and never fails the capture, because
// the record is secondary to the bytes and the client already holds
// them.
func (s *apiServer) recordCapture(e *apiExchange, player *Player, aspect, mediaType string) {
	stamp := s.clock().UTC().Format(time.RFC3339)
	event := &Event{
		APIVersion: "v1",
		Kind:       "Event",
		Metadata: ObjectMeta{
			Name:      player.Metadata.Name + "." + e.id,
			Namespace: player.Metadata.Namespace,
		},
		InvolvedObject: ObjectReference{
			APIVersion: mediaAPIVersion,
			Kind:       "Player",
			Name:       player.Metadata.Name,
			Namespace:  player.Metadata.Namespace,
			UID:        player.Metadata.UID,
		},
		Reason:         capturedReason,
		Type:           normalEventType,
		Message:        e.subject + " took the " + aspect + " of " + player.Metadata.Name + " as " + mediaType,
		Source:         EventSource{Component: eventSourceAPI},
		FirstTimestamp: stamp,
		LastTimestamp:  stamp,
		Count:          1,
	}
	if err := CreateEvent(s.client, event); err != nil {
		fmt.Fprintf(os.Stderr, "writing the Captured event for player %s/%s: %v\n",
			player.Metadata.Namespace, player.Metadata.Name, err)
	}
}

func CreateEvent(c *Client, event *Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	path := podPrefix + event.Metadata.Namespace + eventsPathSegment
	return c.RequestJSON(http.MethodPost, path, body, nil)
}
