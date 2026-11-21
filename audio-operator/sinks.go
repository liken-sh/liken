package main

// A Sink is a playback endpoint and a Source is a capture endpoint.
// Both are cluster-scoped, because the hardware belongs to a machine
// and not to a namespace.
//
// The operator creates an object for each endpoint that it publishes,
// and writes its status, except a Sink's status.session, which the
// media operator writes. The cluster owner declares the endpoint's
// resting level, mute, and other settings in the spec.
//
// These structs have only the fields that this operator reads and
// writes. deploy/crds.yaml has the full schema, and the manual is made
// from its field descriptions.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

// The API group is DriverName, so the DRA driver, its device
// attributes, and these resources share one domain.
const (
	EndpointGroup      = DriverName
	EndpointVersion    = "v1alpha1"
	EndpointAPIVersion = EndpointGroup + "/" + EndpointVersion
	SinksPath          = "/apis/" + EndpointGroup + "/" + EndpointVersion + "/sinks"
	SourcesPath        = "/apis/" + EndpointGroup + "/" + EndpointVersion + "/sources"
)

const (
	SinkKind   = "Sink"
	SourceKind = "Source"
)

// Ready is True when PipeWire has a node for the endpoint, which is the
// fact that the no-sink taint gives the scheduler. Connected is True
// when the endpoint has something attached: a monitor on an HDMI or
// DisplayPort output, a plug in a jack that the card senses, or a
// connected Bluetooth speaker. LayoutApplied exists only on the Sink of
// an ALSA card, and is True when PipeWire runs the channel layout that
// the operator selected (layoutdrift.go).
const (
	ConnectedCondition     = "Connected"
	ReadyCondition         = "Ready"
	LayoutAppliedCondition = "LayoutApplied"
)

// A condition here is True or False and never Unknown, because the
// operator either read the endpoint or it did not.
const (
	conditionTrue  = conditions.True
	conditionFalse = conditions.False
)

type Sink struct {
	APIVersion string       `json:"apiVersion,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	Metadata   EndpointMeta `json:"metadata"`
	Spec       SinkSpec     `json:"spec"`
	Status     SinkStatus   `json:"status,omitempty"`
}

type Source struct {
	APIVersion string         `json:"apiVersion,omitempty"`
	Kind       string         `json:"kind,omitempty"`
	Metadata   EndpointMeta   `json:"metadata"`
	Spec       SourceSpec     `json:"spec"`
	Status     EndpointStatus `json:"status,omitempty"`
}

type EndpointMeta struct {
	Name            string `json:"name"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	// An Event refers to its object by kind, name, and UID. With the
	// UID, a Captured Event belongs to this object and not to a later
	// object that reuses the name.
	UID string `json:"uid,omitempty"`
}

// SinkSpec declares the settings that a playback endpoint rests at.
// Each field is a pointer, a map, or a slice, so that nil means the
// spec declares nothing and the operator writes nothing. A person can
// declare a level of 0 or a mute of false, so a zero value cannot mean
// absent. Codec applies only to Bluetooth speakers. Layout and Headroom
// apply only to ALSA sinks, and an empty layout leaves the layout as
// the hardware has it. SharedClaims is the number of claims that can hold the
// endpoint at once, and the limit is one claim when it is nil
// (shares.go).
type SinkSpec struct {
	Volume       *SinkVolume       `json:"volume,omitempty"`
	Mute         *bool             `json:"mute,omitempty"`
	Controls     map[string]string `json:"controls,omitempty"`
	Codec        *string           `json:"codec,omitempty"`
	Layout       []string          `json:"layout,omitempty"`
	Headroom     *SinkHeadroom     `json:"headroom,omitempty"`
	SharedClaims *int              `json:"sharedClaims,omitempty"`
}

// SinkHeadroom is an object so that a later field can state the
// headroom as a duration beside Samples (headroom.go).
type SinkHeadroom struct {
	Samples int `json:"samples"`
}

// SinkVolume is in percent. This operator writes Level as the resting
// level. Max and Step limit the volume requests that the media operator
// writes into status.session.volumeAsk. This operator reads neither,
// and the API server sets their defaults, 100 and 5, from the CRD.
type SinkVolume struct {
	Level *int `json:"level,omitempty"`
	Max   *int `json:"max,omitempty"`
	Step  *int `json:"step,omitempty"`
}

// SourceSpec has no Codec, because this operator publishes no
// Bluetooth capture endpoint, and no Layout, which applies only to ALSA
// sinks. Volume is the level in percent.
type SourceSpec struct {
	Volume       *int              `json:"volume,omitempty"`
	Mute         *bool             `json:"mute,omitempty"`
	Controls     map[string]string `json:"controls,omitempty"`
	SharedClaims *int              `json:"sharedClaims,omitempty"`
}

// declaration is the part of SinkSpec and SourceSpec that reconcile
// reads, so that one code path handles both kinds.
type declaration struct {
	Volume   *int
	Mute     *bool
	Controls map[string]string
	Codec    *string
}

func (s SinkSpec) declaration() declaration {
	var level *int
	if s.Volume != nil {
		level = s.Volume.Level
	}
	return declaration{Volume: level, Mute: s.Mute, Controls: s.Controls, Codec: s.Codec}
}

func (s SourceSpec) declaration() declaration {
	return declaration{Volume: s.Volume, Mute: s.Mute, Controls: s.Controls}
}

// EndpointStatus is the status of both kinds, so sinkstatus.go builds
// either in one code path. A Source never sets Monitor, Bluetooth,
// Layout, LayoutSource, or Passthrough, because no capture endpoint has
// them.
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
	Layout         []string                     `json:"layout,omitempty"`
	LayoutSource   string                       `json:"layoutSource,omitempty"`
	Passthrough    []string                     `json:"passthrough,omitempty"`
	Observed       *EndpointObserved            `json:"observed,omitempty"`
	Applied        *EndpointApplied             `json:"applied,omitempty"`
	Claims         []EndpointClaim              `json:"claims,omitempty"`
	Conditions     []EndpointCondition          `json:"conditions,omitempty"`
}

// SinkStatus adds Session, which the media operator writes under its
// own field manager. EndpointStatus is a separate type so that a status
// write from this operator has no session field, and cannot set,
// change, or remove the session (endpointreads.go).
type SinkStatus struct {
	EndpointStatus
	Session *SinkSession `json:"session,omitempty"`
}

// SinkSession names the Player that plays through the Sink, and holds
// the latest volume request from a remote's key press.
type SinkSession struct {
	Player    string     `json:"player,omitempty"`
	VolumeAsk *VolumeAsk `json:"volumeAsk,omitempty"`
}

// VolumeAsk is a request for a level in percent and a mute. This
// operator applies each distinct At once (asks.go).
type VolumeAsk struct {
	Level int    `json:"level"`
	Mute  bool   `json:"mute,omitempty"`
	At    string `json:"at"`
}

// EndpointCard is the ALSA card of the endpoint. The number and id can
// differ after a reboot, so nothing durable is keyed to them. Number
// has no omitempty, because 0 is the first card that the kernel
// registers.
type EndpointCard struct {
	Number int    `json:"number"`
	ID     string `json:"id,omitempty"`
	Driver string `json:"driver,omitempty"`
	Name   string `json:"name,omitempty"`
}

// EndpointPCM is the ALSA PCM device of the endpoint. Device has no
// omitempty, because 0 is a valid device.
type EndpointPCM struct {
	Device int    `json:"device"`
	ID     string `json:"id,omitempty"`
}

// EndpointMonitor is the monitor on an HDMI or DisplayPort output,
// read from the ELD that the graphics driver writes for the card.
// Display names the Display that the display operator publishes for the
// same monitor, so a reader can pair the audio endpoint with its
// screen.
type EndpointMonitor struct {
	Display      string `json:"display,omitempty"`
	Manufacturer string `json:"manufacturer,omitempty"`
	Product      string `json:"product,omitempty"`
	Name         string `json:"name,omitempty"`
}

// Peripheral names the Peripheral that the Bluetooth operator
// publishes for the bonded speaker, which is the device address in
// lowercase with dashes.
type EndpointBluetooth struct {
	Address    string   `json:"address,omitempty"`
	Name       string   `json:"name,omitempty"`
	Peripheral string   `json:"peripheral,omitempty"`
	Codec      string   `json:"codec,omitempty"`
	Codecs     []string `json:"codecs,omitempty"`
}

// EndpointFormat is the sample rate, channel count, and channel
// positions that the PipeWire node runs at.
type EndpointFormat struct {
	Rate      int      `json:"rate,omitempty"`
	Channels  int      `json:"channels,omitempty"`
	Positions []string `json:"positions,omitempty"`
}

// EndpointObserved is the last value that the operator read for each
// setting. Only a Bluetooth speaker has a Codec.
type EndpointObserved struct {
	Volume   *int              `json:"volume,omitempty"`
	Mute     *bool             `json:"mute,omitempty"`
	Codec    string            `json:"codec,omitempty"`
	Controls map[string]string `json:"controls,omitempty"`
}

// EndpointApplied records the PipeWire node that the operator last
// checked against the declared volume and mute, and the declaration
// that it checked. After an operator restart, a node with the same
// PipeWire cookie and node id is a node that kept running. A volume
// request or the speaker's own buttons can have changed its level, so
// the operator writes the declaration to it only when the declaration
// differs from Volume and Mute here. Any other node is new, and the
// operator writes the declaration to it, or unity volume when the spec
// declares no level. Volume and Mute are nil when the spec declared no
// level at that check.
type EndpointApplied struct {
	PipeWireCookie uint32 `json:"pipewireCookie"`
	Node           int    `json:"node"`
	Volume         *int   `json:"volume,omitempty"`
	Mute           *bool  `json:"mute,omitempty"`
}

// EndpointClaim names a ResourceClaim that holds the endpoint. The DRA
// plugin adds it when it prepares the claim and removes it when it
// unprepares the claim, so status.claims lists every claim in use.
type EndpointClaim struct {
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name,omitempty"`
}

// EndpointCondition is the condition type of every liken component.
// Its JSON matches the CRD schema, so the API server has nothing to
// prune: lastTransitionTime is an RFC 3339 date-time, an empty message
// is a valid string, and observedGeneration is omitted when it is zero.
type EndpointCondition = conditions.Condition

// setCondition returns a copy of list with next in place of the
// condition of the same type. conditions.Set keeps the transition time
// while the status is unchanged. The copy leaves the published status
// intact, so that sameStatus can compare it with the new one.
func setCondition(list []EndpointCondition, next EndpointCondition) []EndpointCondition {
	updated := slices.Clone(list)
	conditions.Set(&updated, next)
	return updated
}

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
		LastTransitionTime: now.UTC().Truncate(time.Second),
	}
}

func sinkPath(name string) string   { return SinksPath + "/" + name }
func sourcePath(name string) string { return SourcesPath + "/" + name }

func getSink(c *apiclient.Client, name string) (*Sink, error) {
	return apiclient.Get[Sink](c, sinkPath(name))
}

func getSource(c *apiclient.Client, name string) (*Source, error) {
	return apiclient.Get[Source](c, sourcePath(name))
}

// machineSelector is the field selector for resources whose
// status.node is the machine. The CRDs declare status.node a
// selectable field, so the API server filters both the list and the
// watch, and a machine never reads another machine's endpoints.
func machineSelector(machine string) string {
	return "status.node=" + machine
}

func byMachine(path, machine string) string {
	return path + "?fieldSelector=" + url.QueryEscape(machineSelector(machine))
}

// createSink creates a Sink with an empty spec, because the spec is for
// a person or another program to declare, and an empty spec writes
// nothing to the hardware.
func createSink(c *apiclient.Client, name string) (*Sink, error) {
	return post(c, SinksPath, &Sink{
		APIVersion: EndpointAPIVersion,
		Kind:       SinkKind,
		Metadata:   EndpointMeta{Name: name},
	})
}

func createSource(c *apiclient.Client, name string) (*Source, error) {
	return post(c, SourcesPath, &Source{
		APIVersion: EndpointAPIVersion,
		Kind:       SourceKind,
		Metadata:   EndpointMeta{Name: name},
	})
}

// post creates the object and returns the stored copy, which has the
// resourceVersion that the next write needs.
func post[T any](c *apiclient.Client, path string, object *T) (*T, error) {
	body, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	stored := new(T)
	if err := c.RequestJSON(http.MethodPost, path, body, stored); err != nil {
		return nil, err
	}
	return stored, nil
}
