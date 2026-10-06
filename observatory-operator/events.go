package main

// The Events of a Reservation. `kubectl describe reservation` lists
// them under the status, so a person reads when each step ended and
// why a step failed, with no log to open. Each phase change and each
// step's end is one Event, and a failure is a Warning.

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

const (
	eventNormal  = "Normal"
	eventWarning = "Warning"
)

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
	Namespace  string `json:"namespace"`
	UID        string `json:"uid,omitempty"`
}

type eventSource struct {
	Component string `json:"component"`
}

// record writes one Event about a reservation. A failed write is
// logged and changes nothing else: the status holds the same facts.
func (o *operator) record(r *observatory.Reservation, kind, reason, message string) {
	at := time.Now().UTC().Format(time.RFC3339)
	body, err := json.Marshal(event{
		APIVersion: "v1", Kind: "Event",
		Metadata: eventMeta{GenerateName: r.Metadata.Name + ".", Namespace: r.Metadata.Namespace},
		InvolvedObject: involvedObject{
			APIVersion: observatory.APIVersion, Kind: observatory.ReservationKind.Name,
			Name: r.Metadata.Name, Namespace: r.Metadata.Namespace, UID: r.Metadata.UID,
		},
		Reason: reason, Message: message, Type: kind,
		Source:         eventSource{Component: managedBy},
		FirstTimestamp: at, LastTimestamp: at, Count: 1,
	})
	if err == nil {
		err = o.client.RequestJSON(http.MethodPost, "/api/v1/namespaces/"+r.Metadata.Namespace+"/events", body, nil)
	}
	if err != nil {
		o.logf("recording %s on the Reservation %s: %v", reason, r.Metadata.Name, err)
	}
}
