package main

// The record a person reads with kubectl.
//
// Every request that produces bytes writes a Kubernetes Event on the
// Sink or the Source: reason Captured, type Normal, with the caller
// and the aspect in the message, so kubectl describe sink answers who
// listened and when. The log line is the detail record and never
// carries the token.
//
// A Sink and a Source are cluster-scoped and an Event is namespaced,
// so the Event goes in default, which is where Kubernetes puts the
// Events of its own cluster-scoped objects, such as a Node, and where
// kubectl describe finds them.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// capturedReason is the one reason this API writes.
const capturedReason = "Captured"

// eventNamespace is where an Event about a cluster-scoped object
// lands.
const eventNamespace = "default"

// apiComponent is this process's name on liken_build_info and on every
// Event it writes.
const apiComponent = "audio-api"

// event is the part of a core/v1 Event this API writes.
type event struct {
	APIVersion     string         `json:"apiVersion"`
	Kind           string         `json:"kind"`
	Metadata       eventMeta      `json:"metadata"`
	InvolvedObject involvedObject `json:"involvedObject"`
	Reason         string         `json:"reason"`
	Message        string         `json:"message"`
	Type           string         `json:"type"`
	Source         eventSource    `json:"source"`
	FirstTimestamp string         `json:"firstTimestamp"`
	LastTimestamp  string         `json:"lastTimestamp"`
	Count          int            `json:"count"`
}

type eventMeta struct {
	GenerateName string `json:"generateName"`
	Namespace    string `json:"namespace"`
}

type involvedObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid,omitempty"`
}

type eventSource struct {
	Component string `json:"component"`
}

// recordCapture writes one Captured event. A failure here is reported
// and never fails the request: the sound is already on the wire, and
// the log line holds the same record.
func recordCapture(client *Client, kind, name, uid, aspect, format, who string, at time.Time) error {
	stamp := at.UTC().Format(time.RFC3339)
	body, err := json.Marshal(&event{
		APIVersion: "v1",
		Kind:       "Event",
		Metadata: eventMeta{
			GenerateName: name + ".",
			Namespace:    eventNamespace,
		},
		InvolvedObject: involvedObject{
			APIVersion: EndpointAPIVersion,
			Kind:       kind,
			Name:       name,
			UID:        uid,
		},
		Reason:         capturedReason,
		Message:        fmt.Sprintf("%s captured the %s of this %s as %s", who, aspect, kind, format),
		Type:           "Normal",
		Source:         eventSource{Component: apiComponent},
		FirstTimestamp: stamp,
		LastTimestamp:  stamp,
		Count:          1,
	})
	if err != nil {
		return err
	}
	return client.RequestJSON(http.MethodPost,
		"/api/v1/namespaces/"+eventNamespace+"/events", body, nil)
}
