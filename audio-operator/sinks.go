package main

// The two resources: a Sink for every playback endpoint and a Source
// for every capture one.
//
// Both are cluster-scoped, because hardware belongs to a machine and
// not to a tenant. The operator owns them: it creates the object for
// every endpoint it publishes and writes the whole of status, and the
// spec is the cluster owner's declaration of what the endpoint rests
// at. The two kinds share one status shape, because an input and an
// output are described by the same facts, and one status type keeps
// the composition in sinkstatus.go to one path.
//
// These structs hold the fields this operator reads and writes.
// deploy/crds.yaml is the whole schema, and the descriptions there
// are the manual.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// The API group is the driver's own name, so one domain names the
// driver, the attributes, and these two resources.
const (
	EndpointGroup      = DriverName
	EndpointVersion    = "v1alpha1"
	EndpointAPIVersion = EndpointGroup + "/" + EndpointVersion
	SinksPath          = "/apis/" + EndpointGroup + "/" + EndpointVersion + "/sinks"
	SourcesPath        = "/apis/" + EndpointGroup + "/" + EndpointVersion + "/sources"
)

// The two kinds, spelled as the API server names them.
const (
	SinkKind   = "Sink"
	SourceKind = "Source"
)

// The two conditions. Connected reports that the endpoint can play
// or record now, and Ready reports that PipeWire holds a node for it.
// They carry the same facts as the no-monitor and no-sink taints, for
// a person rather than the scheduler.
const (
	ConnectedCondition = "Connected"
	ReadyCondition     = "Ready"
)

// The two states a condition takes here. Unknown is never written:
// the operator either read the endpoint or it did not.
const (
	conditionTrue  = "True"
	conditionFalse = "False"
)

// Sink is one playback endpoint.
type Sink struct {
	APIVersion string         `json:"apiVersion,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Metadata   EndpointMeta   `json:"metadata"`
	Spec       SinkSpec       `json:"spec"`
	Status     EndpointStatus `json:"status,omitempty"`
}

type SinkList struct {
	Items []Sink `json:"items"`
}

// Source is one capture endpoint.
type Source struct {
	APIVersion string         `json:"apiVersion,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Metadata   EndpointMeta   `json:"metadata"`
	Spec       SourceSpec     `json:"spec"`
	Status     EndpointStatus `json:"status,omitempty"`
}

type SourceList struct {
	Items []Source `json:"items"`
}

type EndpointMeta struct {
	Name            string `json:"name"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}

// SinkSpec is what a playback endpoint rests at.
//
// Every field is a pointer because its absence is what says the
// operator writes nothing, and zero is a level and a mute a person
// declares. Codec is a speaker's alone, and an ALSA endpoint ignores
// it.
type SinkSpec struct {
	Volume   *int              `json:"volume,omitempty"`
	Mute     *bool             `json:"mute,omitempty"`
	Controls map[string]string `json:"controls,omitempty"`
	Codec    *string           `json:"codec,omitempty"`
}

// SourceSpec is the same declaration for a capture endpoint, without
// the codec: no capture endpoint this operator publishes is on a
// radio.
type SourceSpec struct {
	Volume   *int              `json:"volume,omitempty"`
	Mute     *bool             `json:"mute,omitempty"`
	Controls map[string]string `json:"controls,omitempty"`
}

// declaration is the part of a spec both kinds share, so that one
// reconcile reads either kind.
type declaration struct {
	Volume   *int
	Mute     *bool
	Controls map[string]string
	Codec    *string
}

func (s SinkSpec) declaration() declaration {
	return declaration{Volume: s.Volume, Mute: s.Mute, Controls: s.Controls, Codec: s.Codec}
}

func (s SourceSpec) declaration() declaration {
	return declaration{Volume: s.Volume, Mute: s.Mute, Controls: s.Controls}
}

// EndpointStatus is what the hardware declares and what the operator
// last read. Both kinds carry this shape, and the two fields that
// belong to a Sink alone, monitor and bluetooth, are absent on a
// Source because no capture endpoint has either.
type EndpointStatus struct {
	Node           string                       `json:"node,omitempty"`
	Location       string                       `json:"location,omitempty"`
	ConnectionType string                       `json:"connectionType,omitempty"`
	Card           *EndpointCard                `json:"card,omitempty"`
	PCM            *EndpointPCM                 `json:"pcm,omitempty"`
	Monitor        *EndpointMonitor             `json:"monitor,omitempty"`
	Bluetooth      *EndpointBluetooth           `json:"bluetooth,omitempty"`
	NodeName       string                       `json:"nodeName,omitempty"`
	Format         *EndpointFormat              `json:"format,omitempty"`
	Capabilities   map[string]controlCapability `json:"capabilities,omitempty"`
	Observed       *EndpointObserved            `json:"observed,omitempty"`
	Claim          *EndpointClaim               `json:"claim,omitempty"`
	Conditions     []EndpointCondition          `json:"conditions,omitempty"`
}

// EndpointCard is the ALSA card the endpoint is on. The number and
// the id are this boot's, so nothing durable is keyed to them, and
// the number is written even when it is zero because zero is the
// first card the kernel registers.
type EndpointCard struct {
	Number int    `json:"number"`
	ID     string `json:"id,omitempty"`
	Driver string `json:"driver,omitempty"`
	Name   string `json:"name,omitempty"`
}

// EndpointPCM is the PCM device the endpoint runs through. The device
// number is written even when it is zero, for the card number's
// reason.
type EndpointPCM struct {
	Device int    `json:"device"`
	ID     string `json:"id,omitempty"`
}

// EndpointMonitor is the monitor an HDMI or DisplayPort slot feeds,
// from the ELD block the graphics driver wrote into the card. Display
// is the pairing identity, which is the name the display operator
// publishes its own Display under.
type EndpointMonitor struct {
	Display      string `json:"display,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Product      string `json:"product,omitempty"`
	Name         string `json:"name,omitempty"`
}

// EndpointBluetooth is the speaker behind a Bluetooth endpoint.
// Pairing is the name the Bluetooth operator's own Pairing carries,
// which is the address in lowercase with dashes.
type EndpointBluetooth struct {
	Address string   `json:"address,omitempty"`
	Name    string   `json:"name,omitempty"`
	Pairing string   `json:"pairing,omitempty"`
	Codec   string   `json:"codec,omitempty"`
	Codecs  []string `json:"codecs,omitempty"`
}

// EndpointFormat is what the node negotiated and runs at now.
type EndpointFormat struct {
	Rate      int      `json:"rate,omitempty"`
	Channels  int      `json:"channels,omitempty"`
	Positions []string `json:"positions,omitempty"`
}

// EndpointObserved is the last value the operator read for each
// setting. Codec is a speaker's alone.
type EndpointObserved struct {
	Volume   *int              `json:"volume,omitempty"`
	Mute     *bool             `json:"mute,omitempty"`
	Codec    string            `json:"codec,omitempty"`
	Controls map[string]string `json:"controls,omitempty"`
}

// EndpointClaim names the claim that holds the endpoint now. The DRA
// plugin records it at prepare and drops it at unprepare, so the field
// answers which workload has the speakers.
type EndpointClaim struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
}

// EndpointCondition is the standard condition shape, held here for
// the reason the slice structs are held here: this program writes
// these fields and no others.
type EndpointCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime"`
}

// setCondition replaces the condition of one type and keeps the
// timestamp when nothing about it changed. A timestamp that moved on
// every pass would make every pass a write.
func setCondition(conditions []EndpointCondition, next EndpointCondition) []EndpointCondition {
	for index, current := range conditions {
		if current.Type != next.Type {
			continue
		}
		if current.Status == next.Status && current.Reason == next.Reason && current.Message == next.Message {
			return conditions
		}
		if current.Status == next.Status {
			next.LastTransitionTime = current.LastTransitionTime
		}
		updated := make([]EndpointCondition, len(conditions))
		copy(updated, conditions)
		updated[index] = next
		return updated
	}
	return append(conditions, next)
}

// condition builds one condition from the fact it reports.
func condition(kind string, met bool, reason, message string, now time.Time) EndpointCondition {
	status := conditionFalse
	if met {
		status = conditionTrue
	}
	return EndpointCondition{
		Type:               kind,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now.UTC().Format(time.RFC3339),
	}
}

func getSink(c *Client, name string) (*Sink, error) { return get[Sink](c, SinksPath+"/"+name) }

func getSource(c *Client, name string) (*Source, error) {
	return get[Source](c, SourcesPath+"/"+name)
}

func listSinks(c *Client) ([]Sink, error) {
	list, err := get[SinkList](c, SinksPath)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

func listSources(c *Client) ([]Source, error) {
	list, err := get[SourceList](c, SourcesPath)
	if err != nil {
		return nil, err
	}
	return list.Items, nil
}

// The create carries an empty spec. The operator declares nothing
// about how an endpoint should rest: the resource exists so that a
// person or a machine writer can, and an empty spec writes nothing to
// the hardware.
func createSink(c *Client, name string) (*Sink, error) {
	return post(c, SinksPath, &Sink{
		APIVersion: EndpointAPIVersion,
		Kind:       SinkKind,
		Metadata:   EndpointMeta{Name: name},
	})
}

func createSource(c *Client, name string) (*Source, error) {
	return post(c, SourcesPath, &Source{
		APIVersion: EndpointAPIVersion,
		Kind:       SourceKind,
		Metadata:   EndpointMeta{Name: name},
	})
}

// The status write goes to the status subresource, so a spec a person
// edited between the read and the write is not overwritten.
func writeSinkStatus(c *Client, sink *Sink, status EndpointStatus) (*Sink, error) {
	written := *sink
	written.APIVersion, written.Kind = EndpointAPIVersion, SinkKind
	written.Status = status
	return put(c, SinksPath+"/"+sink.Metadata.Name+"/status", &written)
}

func writeSourceStatus(c *Client, source *Source, status EndpointStatus) (*Source, error) {
	written := *source
	written.APIVersion, written.Kind = EndpointAPIVersion, SourceKind
	written.Status = status
	return put(c, SourcesPath+"/"+source.Metadata.Name+"/status", &written)
}

// post creates one object and answers with what the API server
// stored, which carries the resourceVersion the next write needs.
func post[T any](c *Client, path string, object *T) (*T, error) {
	return send[T](c, http.MethodPost, path, object)
}

// put replaces one object.
func put[T any](c *Client, path string, object *T) (*T, error) {
	return send[T](c, http.MethodPut, path, object)
}

func send[T any](c *Client, method, path string, object *T) (*T, error) {
	body, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	stored := new(T)
	if err := c.RequestJSON(method, path, body, stored); err != nil {
		return nil, err
	}
	return stored, nil
}

// endpointWatchTimeout is how long one watch connection lives before
// the API server closes it and the operator opens another. A watch
// that never ends holds a connection through every network fault in
// between.
const endpointWatchTimeout = 290 * time.Second

// endpointWatchRetry is how long the operator waits before it opens
// the watch again.
const endpointWatchRetry = 5 * time.Second

// watchEndpoints turns a spec that changed into one wake, on both
// collections. Nothing of the event is read but its arrival: the pass
// that follows reads every endpoint again, the way every other wake in
// this operator works.
func watchEndpoints(ctx context.Context, c *Client, wake func()) {
	go watchCollection(ctx, c, SinksPath, wake)
	go watchCollection(ctx, c, SourcesPath, wake)
}

func watchCollection(ctx context.Context, c *Client, path string, wake func()) {
	for ctx.Err() == nil {
		if err := streamEvents(ctx, c, path, wake); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "watching %s: %v\n", path, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(endpointWatchRetry):
		}
	}
}

// streamEvents holds one watch connection. It starts at the present,
// because an event carries nothing the pass uses and a missed event
// costs one backstop tick.
func streamEvents(ctx context.Context, c *Client, path string, wake func()) error {
	body, err := c.Watch(ctx, fmt.Sprintf("%s?watch=true&timeoutSeconds=%d",
		path, int(endpointWatchTimeout.Seconds())))
	if err != nil {
		return err
	}
	defer drain(body)

	events := json.NewDecoder(body)
	for {
		var event struct {
			Type string `json:"type"`
		}
		if err := events.Decode(&event); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		wake()
	}
}
