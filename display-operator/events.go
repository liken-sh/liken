package main

// This file writes the record a capture leaves behind. An Event on
// the Display is the record, because kubectl describe display then
// answers who looked at a screen and when with no log to open, in
// the same place every other fact about the Display is read. The
// API's log line is the detail beside it: the route, the status, the
// bytes, the duration, and the request id.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

// An Event is namespaced and a Display is not, so its Events live in
// default, the convention a Node's own Events follow.
const eventNamespace = "default"

// Every capture Event carries the reason Captured and the type
// Normal, with the subject and the aspect in its message.
const (
	capturedReason = "Captured"
	normalEvent    = "Normal"
)

// The fields of an Event this program writes, in the style the rest
// of this repository writes a resource.
type Event struct {
	APIVersion     string         `json:"apiVersion"`
	Kind           string         `json:"kind"`
	Metadata       EventMeta      `json:"metadata"`
	InvolvedObject InvolvedObject `json:"involvedObject"`
	Reason         string         `json:"reason"`
	Message        string         `json:"message"`
	Type           string         `json:"type"`
	Source         EventSource    `json:"source"`
	FirstTimestamp string         `json:"firstTimestamp"`
	LastTimestamp  string         `json:"lastTimestamp"`
	Count          int            `json:"count"`
}

type EventMeta struct {
	GenerateName string `json:"generateName"`
	Namespace    string `json:"namespace"`
}

type InvolvedObject struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	UID        string `json:"uid,omitempty"`
}

type EventSource struct {
	Component string `json:"component"`
}

// A failed Event write is reported to the log and never fails the
// request, because the bytes already reached the caller.
func recordCapture(c *Client, screen *Display, subject, aspect, form string) {
	name := screen.Metadata.Name
	at := time.Now().UTC().Format(time.RFC3339)
	body, err := json.Marshal(Event{
		APIVersion: "v1",
		Kind:       "Event",
		Metadata: EventMeta{
			GenerateName: name + ".",
			Namespace:    eventNamespace,
		},
		InvolvedObject: InvolvedObject{
			APIVersion: DisplayAPIVersion,
			Kind:       "Display",
			Name:       name,
			UID:        screen.Metadata.UID,
		},
		Reason:         capturedReason,
		Message:        fmt.Sprintf("%s took the %s of %s as %s", subject, aspect, name, form),
		Type:           normalEvent,
		Source:         EventSource{Component: apiComponent},
		FirstTimestamp: at,
		LastTimestamp:  at,
		Count:          1,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "writing the %s event for %s: %v\n", capturedReason, name, err)
		return
	}
	path := "/api/v1/namespaces/" + eventNamespace + "/events"
	if err := c.RequestJSON(http.MethodPost, path, body, nil); err != nil {
		fmt.Fprintf(os.Stderr, "writing the %s event for %s: %v\n", capturedReason, name, err)
	}
}
