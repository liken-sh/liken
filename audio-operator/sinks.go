package main

// The two resources: a Sink for every playback endpoint and a Source
// for every capture one.
//
// Both are cluster-scoped, because hardware belongs to a machine and
// not to a tenant. The operator owns them: it creates the object for
// every endpoint it publishes and writes the status, apart from a
// Sink's status.session, which the media operator writes. The spec is
// the cluster owner's declaration of what the endpoint rests at. The
// two kinds share one status shape, because an input and an
// output are described by the same facts, and one status type keeps
// the composition in sinkstatus.go to one path.
//
// These structs hold the fields this operator reads and writes.
// deploy/crds.yaml is the whole schema, and the descriptions there
// are the manual.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
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

// The conditions. Connected reports that the endpoint can play or
// record now, and Ready reports that PipeWire holds a node for it.
// They carry the same facts as the no-monitor and no-sink taints, for
// a person rather than the scheduler. LayoutApplied is a card's
// sink's alone, and reports whether PipeWire runs the channel layout
// the operator selected for it (layoutdrift.go).
const (
	ConnectedCondition     = "Connected"
	ReadyCondition         = "Ready"
	LayoutAppliedCondition = "LayoutApplied"
)

// The two states a condition takes here. Unknown is never written:
// the operator either read the endpoint or it did not.
const (
	conditionTrue  = conditions.True
	conditionFalse = conditions.False
)

// Sink is one playback endpoint.
type Sink struct {
	APIVersion string       `json:"apiVersion,omitempty"`
	Kind       string       `json:"kind,omitempty"`
	Metadata   EndpointMeta `json:"metadata"`
	Spec       SinkSpec     `json:"spec"`
	Status     SinkStatus   `json:"status,omitempty"`
}

// Source is one capture endpoint.
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
	// An Event names the object it is about by kind, name, and UID,
	// so a Captured event follows the endpoint it was written on and
	// not a later object of the same name.
	UID string `json:"uid,omitempty"`
}

// SinkSpec is what a playback endpoint rests at.
//
// Every field is a pointer because its absence is what says the
// operator writes nothing, and zero is a level and a mute a person
// declares. Codec is a speaker's alone, and an ALSA endpoint ignores
// it. Layout is an ALSA sink's alone, and an empty list is the same
// as no list: either one leaves the layout to the hardware.
type SinkSpec struct {
	Volume   *SinkVolume       `json:"volume,omitempty"`
	Mute     *bool             `json:"mute,omitempty"`
	Controls map[string]string `json:"controls,omitempty"`
	Codec    *string           `json:"codec,omitempty"`
	Layout   []string          `json:"layout,omitempty"`
}

// SinkVolume is a Sink's volume declaration, all in percent. Level is
// the resting level this operator applies. Max and Step are the bounds
// the media operator steps its volume asks by, and this operator reads
// neither: the API server fills their defaults.
type SinkVolume struct {
	Level *int `json:"level,omitempty"`
	Max   *int `json:"max,omitempty"`
	Step  *int `json:"step,omitempty"`
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
	var level *int
	if s.Volume != nil {
		level = s.Volume.Level
	}
	return declaration{Volume: level, Mute: s.Mute, Controls: s.Controls, Codec: s.Codec}
}

func (s SourceSpec) declaration() declaration {
	return declaration{Volume: s.Volume, Mute: s.Mute, Controls: s.Controls}
}

// EndpointStatus is what the hardware declares and what the operator
// last read. Both kinds carry this shape, and the fields that belong
// to a Sink alone, monitor, bluetooth, layout, and layoutSource, are
// absent on a Source because no capture endpoint has them.
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
	Observed       *EndpointObserved            `json:"observed,omitempty"`
	Claim          *EndpointClaim               `json:"claim,omitempty"`
	Conditions     []EndpointCondition          `json:"conditions,omitempty"`
}

// SinkStatus is a Sink's status: the endpoint's facts, which this
// operator writes, and the session block, which the media operator
// writes under its own field manager. The facts are a type of their
// own so that every status write this operator composes holds no
// session, and a write of the facts can never state, change, or remove
// the session (endpointreads.go).
type SinkStatus struct {
	EndpointStatus
	Session *SinkSession `json:"session,omitempty"`
}

// SinkSession is the media operator's block: the Player whose unit
// uses the Sink, and the last volume ask a press made.
type SinkSession struct {
	Player    string     `json:"player,omitempty"`
	VolumeAsk *VolumeAsk `json:"volumeAsk,omitempty"`
}

// VolumeAsk is one ask for a level and a mute, in percent. At names the
// ask: each new time is one ask, applied once (asks.go).
type VolumeAsk struct {
	Level int    `json:"level"`
	Mute  bool   `json:"mute,omitempty"`
	At    string `json:"at"`
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
// Peripheral is the name of the Peripheral the Bluetooth operator
// publishes for the bonded device, which is the address in lowercase
// with dashes.
type EndpointBluetooth struct {
	Address    string   `json:"address,omitempty"`
	Name       string   `json:"name,omitempty"`
	Peripheral string   `json:"peripheral,omitempty"`
	Codec      string   `json:"codec,omitempty"`
	Codecs     []string `json:"codecs,omitempty"`
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

// EndpointCondition is the condition type every liken component
// reports. Its JSON matches the CRDs' schema: lastTransitionTime is an
// RFC 3339 date-time, an empty message is a valid string, and the
// schema prunes nothing, because this operator states no
// observedGeneration and the field is omitted when it is zero.
type EndpointCondition = conditions.Condition

// setCondition answers the list with next in place of the condition
// of its type, and keeps the transition time while the status holds
// (conditions.Set). It answers a copy, because the caller composes
// from the published status, and sameStatus compares the two.
func setCondition(list []EndpointCondition, next EndpointCondition) []EndpointCondition {
	updated := slices.Clone(list)
	conditions.Set(&updated, next)
	return updated
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

// machineSelector is the field selector that takes the resources whose
// status.node is one machine. The CRDs declare status.node as a
// selectable field, so the API server filters the list and the watch,
// and a machine reads nothing about another machine's hardware.
func machineSelector(machine string) string {
	return "status.node=" + machine
}

// byMachine narrows a list of Sinks or Sources to the ones whose
// status.node is one machine, the selection the watches hold.
func byMachine(path, machine string) string {
	return path + "?fieldSelector=" + url.QueryEscape(machineSelector(machine))
}

// The create carries an empty spec. The operator declares nothing
// about how an endpoint should rest: the resource exists so that a
// person or a machine writer can, and an empty spec writes nothing to
// the hardware.
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

// post creates one object and answers with what the API server
// stored, which carries the resourceVersion the next write needs.
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
