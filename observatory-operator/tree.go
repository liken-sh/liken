package main

// The tree is one reading of the observatory's resources and of the
// objects the operator created, taken from the watches' stores. Each
// resource names its parent in its spec, so every reference points up
// the tree, and the tree answers the questions that point down: which
// devices a telescope's server runs, and which trains a tube serves.

import (
	"encoding/json"
	"strings"

	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// deviceObject is one device resource of any of the 14 device kinds.
// The spec holds every field that any device kind has, so one type
// reads them all, and the status holds the readings of the kind as
// JSON. The CRD of each kind refuses the fields it does not have, so a
// field that does not belong to a kind is always empty here.
type deviceObject struct {
	APIVersion string                 `json:"apiVersion,omitempty"`
	Kind       string                 `json:"kind,omitempty"`
	Metadata   observatory.ObjectMeta `json:"metadata"`
	Spec       deviceSpec             `json:"spec"`
	Status     deviceStatus           `json:"status,omitzero"`
}

func (d *deviceObject) GetObjectMeta() memo.Meta { return &d.Metadata }

type deviceSpec struct {
	Telescope    string `json:"telescope,omitempty"`
	OpticalTrain string `json:"opticalTrain,omitempty"`
	Observatory  string `json:"observatory,omitempty"`
	observatory.DeviceSpec
	Gain        *float64 `json:"gain,omitempty"`
	Offset      *float64 `json:"offset,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	Filters     []string `json:"filters,omitempty"`
}

type deviceStatus struct {
	observatory.DeviceStatus
	Readings any `json:"readings,omitempty"`
}

// device is one device resource and its kind.
type device struct {
	kind   observatory.Kind
	object deviceObject
}

func (d *device) name() string { return d.object.Metadata.Name }

// key names the device across kinds, such as Camera/east-main.
func (d *device) key() string { return d.kind.Name + "/" + d.name() }

// serverRef names one INDI server: a Telescope's, or an Observatory's
// for the devices that no telescope owns.
type serverRef struct {
	kind observatory.Kind
	name string
}

func (s serverRef) String() string { return strings.ToLower(s.kind.Name) + "-" + s.name }

type tree struct {
	namespace     string
	observatories map[string]*observatory.Observatory
	telescopes    map[string]*observatory.Telescope
	tubes         map[string]*observatory.OpticalTube
	trains        map[string]*observatory.OpticalTrain
	guiders       map[string]*observatory.Guider
	reservations  map[string]*observatory.Reservation
	// devices are sorted by kind, in the order of
	// observatory.DeviceKinds, and then by name.
	devices  []*device
	pods     map[string]*pod
	services map[string]*service
	claims   map[string]bool
}

// parent answers the resource above a device, from the parent field
// its kind has.
func (d *device) parent() observatory.Parent {
	s := d.object.Spec
	switch {
	case s.OpticalTrain != "":
		return observatory.Parent{Kind: observatory.OpticalTrainKind, Name: s.OpticalTrain}
	case s.Telescope != "":
		return observatory.Parent{Kind: observatory.TelescopeKind, Name: s.Telescope}
	}
	return observatory.Parent{Kind: observatory.ObservatoryKind, Name: s.Observatory}
}

// server answers the INDI server that runs a device: its telescope's,
// or its observatory's. A device on a train runs on the server of the
// train's telescope. The answer is false while a parent is missing.
func (t *tree) server(d *device) (serverRef, bool) {
	p := d.parent()
	switch p.Kind {
	case observatory.OpticalTrainKind:
		train, ok := t.trains[p.Name]
		if !ok {
			return serverRef{}, false
		}
		return serverRef{observatory.TelescopeKind, train.Spec.Telescope}, true
	case observatory.TelescopeKind:
		return serverRef{observatory.TelescopeKind, p.Name}, true
	}
	return serverRef{observatory.ObservatoryKind, p.Name}, true
}

// devicesOn answers the devices that one server runs, in the order of
// t.devices.
func (t *tree) devicesOn(s serverRef) []*device {
	var out []*device
	for _, d := range t.devices {
		if on, ok := t.server(d); ok && on == s {
			out = append(out, d)
		}
	}
	return out
}

// devicesOf answers the devices of one kind on one server.
func (t *tree) devicesOf(s serverRef, kind observatory.Kind) []*device {
	var out []*device
	for _, d := range t.devicesOn(s) {
		if d.kind == kind {
			out = append(out, d)
		}
	}
	return out
}

// trainDevices answers the devices whose spec.opticalTrain names a
// train.
func (t *tree) trainDevices(train string) []*device {
	var out []*device
	for _, d := range t.devices {
		if d.object.Spec.OpticalTrain == train {
			out = append(out, d)
		}
	}
	return out
}

// device answers the device of one kind and name.
func (t *tree) device(kind observatory.Kind, name string) (*device, bool) {
	for _, d := range t.devices {
		if d.kind == kind && d.name() == name {
			return d, true
		}
	}
	return nil, false
}

// missingParent answers the first resource up the tree from a device
// that does not exist, as "the OpticalTrain east-imaging", or "" when
// every parent up to the Observatory exists.
func (t *tree) missingParent(d *device) string {
	p := d.parent()
	switch p.Kind {
	case observatory.OpticalTrainKind:
		train, ok := t.trains[p.Name]
		if !ok {
			return "the OpticalTrain " + p.Name
		}
		return t.missingTelescope(train.Spec.Telescope)
	case observatory.TelescopeKind:
		return t.missingTelescope(p.Name)
	}
	return t.missingObservatory(p.Name)
}

func (t *tree) missingTelescope(name string) string {
	telescope, ok := t.telescopes[name]
	if !ok {
		return "the Telescope " + name
	}
	return t.missingObservatory(telescope.Spec.Observatory)
}

func (t *tree) missingObservatory(name string) string {
	if _, ok := t.observatories[name]; !ok {
		return "the Observatory " + name
	}
	return ""
}

// asJSON answers a value as the generic form that a decoded JSON
// document has, so two statuses compare equal when the API server
// would store the same document. A number read from a watch's store is
// an int64 and the same number composed by the operator is a float64,
// and this form makes both float64.
func asJSON(v any) any {
	body, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	_ = json.Unmarshal(body, &out)
	return out
}
