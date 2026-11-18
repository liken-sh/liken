package main

// A tree is a snapshot of the observatory's resources and of the
// objects the operator created, built from the watch caches. Each
// resource names its parent in its spec, so a lookup in the other
// direction, such as which devices run on a telescope's INDI server, is
// a method of tree that scans the children.

import (
	"encoding/json"
	"slices"
	"sync"

	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// deviceObject decodes a device resource of any kind. The status keeps
// the fields that differ by kind as values of type any. The CRD of each
// kind rejects the fields that the kind does not have, so such a field
// is always empty here.
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
	observatory.Procedures[action]
	FITSKeywords []observatory.FITSKeyword `json:"fitsKeywords,omitempty"`
	// raw is the spec as the API server stores it. The Configure step
	// decodes the kind's settings from raw into the kind's own type
	// (settings.go).
	raw []byte
}

// UnmarshalJSON keeps the whole spec in raw for the kind's settings.
func (s *deviceSpec) UnmarshalJSON(body []byte) error {
	type shared deviceSpec
	if err := json.Unmarshal(body, (*shared)(s)); err != nil {
		return err
	}
	s.raw = slices.Clone(body)
	return nil
}

type deviceStatus struct {
	observatory.DeviceStatus
	// Standard is the kind's typed standard properties, such as a
	// *observatory.CameraStandard. MarshalJSON writes its fields at the
	// top level of the status, where the kind's status type embeds
	// them. A device in the watch cache has a nil Standard (watch.go).
	Standard any `json:"-"`
	Readings any `json:"readings,omitempty"`
	Display  any `json:"display,omitempty"`
}

// MarshalJSON writes the fields of Standard at the top level of the
// status JSON, as encoding/json would write an embedded struct.
func (s deviceStatus) MarshalJSON() ([]byte, error) {
	type plain deviceStatus
	shared, err := json.Marshal(plain(s))
	if err != nil || s.Standard == nil {
		return shared, err
	}
	typed, err := json.Marshal(s.Standard)
	switch {
	case err != nil:
		return nil, err
	case len(typed) <= len("{}"):
		return shared, nil
	case len(shared) <= len("{}"):
		return typed, nil
	}
	return append(append(shared[:len(shared)-1], ','), typed[1:]...), nil
}

// device is a device resource and its kind. While the device is in a
// session's record, object.Spec is the spec that the session recorded,
// not the spec a person wrote (records.go).
type device struct {
	kind   observatory.Kind
	object deviceObject
	// declared is the spec as the API server stores it. It is set only
	// when the recorded spec replaced object.Spec.
	declared *deviceSpec
	// record is the device's entry in a session record, and recordedOn
	// is the server of that session.
	record     *observatory.SessionDevice
	recordedOn serverRef
}

// declaredSpec returns the spec as a person wrote it.
func (d *device) declaredSpec() *deviceSpec {
	if d.declared != nil {
		return d.declared
	}
	return &d.object.Spec
}

func (d *device) name() string { return d.object.Metadata.Name }

// key identifies the device across kinds, such as Camera/east-main.
func (d *device) key() string { return d.kind.Name + "/" + d.name() }

// serverRef identifies one INDI server: a Telescope's, or an
// Observatory's for the devices that belong to no telescope.
type serverRef struct {
	kind observatory.Kind
	name string
}

// String returns the name of the server's pod and Service. The
// operator also keys its INDI connections by this name.
func (s serverRef) String() string { return generatedName(s.kind, s.name) }

// tree is a copy of every watched object. Every goroutine that reads
// between two changes gets the same tree (operator.snapshot), so code
// must not modify a tree or an object in it. Copy an object before you
// change it.
type tree struct {
	namespace     string
	observatories map[string]*observatory.Observatory
	telescopes    map[string]*observatory.Telescope
	tubes         map[string]*observatory.OpticalTube
	trains        map[string]*observatory.OpticalTrain
	guiders       map[string]*observatory.Guider
	reservations  map[string]*observatory.Reservation
	horizons      map[string]*observatory.Horizon
	// devices are sorted by kind, in the order of
	// observatory.DeviceKinds, and then by name.
	devices    []*device
	pods       map[string]*pod
	services   map[string]*service
	configMaps map[string]*configMap
	jobs       map[string]*job
	claims     map[string]bool

	// driverHolders is computed on first use (shareddrivers.go).
	driverHoldersOnce sync.Once
	driverHolders     map[*device]*device
	// held is computed on first use (sessions).
	heldOnce sync.Once
	held     sessions
	// served has the owner of each served device (served.go).
	served servedOwnerships
	// records has the session record of each held server, from the
	// status of the reservation that holds it. recordedTubes has the
	// recorded spec of each tube in a record. writtenTrains has each
	// train whose spec a record replaced, with the spec that a person
	// wrote (records.go).
	records       map[serverRef]observatory.Session
	recordedTubes map[string]observatory.SessionTube
	writtenTrains map[string]*observatory.OpticalTrain
}

// parent returns the resource above a device. It returns false for a
// device that names no parent, which the status calls not installed.
func (d *device) parent() (observatory.Parent, bool) { return parentOf(&d.object.Spec) }

// declaredParent returns the parent in the spec that a person wrote.
func (d *device) declaredParent() (observatory.Parent, bool) { return parentOf(d.declaredSpec()) }

func parentOf(s *deviceSpec) (observatory.Parent, bool) {
	switch {
	case s.OpticalTrain != "":
		return observatory.Parent{Kind: observatory.OpticalTrainKind, Name: s.OpticalTrain}, true
	case s.Telescope != "":
		return observatory.Parent{Kind: observatory.TelescopeKind, Name: s.Telescope}, true
	case s.Observatory != "":
		return observatory.Parent{Kind: observatory.ObservatoryKind, Name: s.Observatory}, true
	}
	return observatory.Parent{}, false
}

// declared returns the INDI server that the specs a person wrote put
// the device on: its telescope's server or its observatory's. A device
// on a train goes to the telescope in the train's current spec, even
// when a session recorded an older spec for the train. It returns false
// for a device with no parent, a device whose train does not exist, and
// a deleted device. The finalizer keeps a deleted device's object in
// the API, and the server that runs it treats it as a device that left
// (finalizers.go).
func (t *tree) declared(d *device) (serverRef, bool) {
	p, installed := d.declaredParent()
	switch {
	case !installed, deleting(d.object.Metadata):
		return serverRef{}, false
	case p.Kind == observatory.OpticalTrainKind:
		train, ok := t.trainAsWritten(p.Name)
		if !ok {
			return serverRef{}, false
		}
		return serverRef{observatory.TelescopeKind, train.Spec.Telescope}, true
	case p.Kind == observatory.TelescopeKind:
		return serverRef{observatory.TelescopeKind, p.Name}, true
	}
	return serverRef{observatory.ObservatoryKind, p.Name}, true
}

// runningOn returns the server of the pod that runs a device's driver
// (podOf), from the pod's server label. A served device uses its
// owner's pod while it shares the owner's INDI device. Unlike declared,
// it ignores the device's parents and its deletion, because a device's
// deactivation runs through the driver that is running. That includes a
// device that a person deleted, and one whose finalizer a person
// removed by hand.
func (t *tree) runningOn(d *device) (serverRef, bool) {
	_, p, running := t.podOf(d)
	if !running {
		return serverRef{}, false
	}
	return t.serverNamed(p.Metadata.Labels[labelServer])
}

// devicesRunningOn returns the devices whose driver pod belongs to s
// (runningOn), in the order of t.devices, even when the device's spec
// now names other parents.
// These are the devices whose deactivation the server can run.
func (t *tree) devicesRunningOn(s serverRef) []*device {
	var out []*device
	for _, d := range t.devices {
		if on, ok := t.runningOn(d); ok && on == s {
			out = append(out, d)
		}
	}
	return out
}

// releasedOn returns the devices that the release of s stops, in the
// order of t.devices: the devices whose driver pod belongs to s, and
// the devices in the session record of s, whose pods the release can
// already have deleted. A device that its parents put on s but that the
// session never started is not included.
func (t *tree) releasedOn(s serverRef) []*device {
	var out []*device
	for _, d := range t.devices {
		on, running := t.runningOn(d)
		if (running && on == s) || (d.record != nil && d.recordedOn == s) {
			out = append(out, d)
		}
	}
	return out
}

// serverNamed returns the server of a Telescope or an Observatory in
// the tree whose pod has this name.
func (t *tree) serverNamed(name string) (serverRef, bool) {
	for scope := range t.telescopes {
		if ref := (serverRef{observatory.TelescopeKind, scope}); ref.String() == name {
			return ref, true
		}
	}
	for site := range t.observatories {
		if ref := (serverRef{observatory.ObservatoryKind, site}); ref.String() == name {
			return ref, true
		}
	}
	return serverRef{}, false
}

// devicesOn returns the devices that s runs, in the order of
// t.devices. It leaves out a device whose driver another device on s
// already runs (shareddrivers.go), and a served device that no device
// on s serves (served.go).
func (t *tree) devicesOn(s serverRef) []*device {
	var out []*device
	for _, d := range t.placedOn(s) {
		if _, owned := t.ownerOf(d); !owned {
			continue
		}
		if _, taken := t.driverHolder(d); !taken {
			out = append(out, d)
		}
	}
	return out
}

// placedOn returns the devices that t.server puts on s, in the order of
// t.devices, including those that devicesOn leaves out.
func (t *tree) placedOn(s serverRef) []*device {
	var out []*device
	for _, d := range t.devices {
		if on, ok := t.server(d); ok && on == s {
			out = append(out, d)
		}
	}
	return out
}

// devicesOf returns the devices of one kind that s runs.
func (t *tree) devicesOf(s serverRef, kind observatory.Kind) []*device {
	var out []*device
	for _, d := range t.devicesOn(s) {
		if d.kind == kind {
			out = append(out, d)
		}
	}
	return out
}

func (t *tree) trainDevices(train string) []*device {
	var out []*device
	for _, d := range t.devices {
		if d.object.Spec.OpticalTrain == train {
			out = append(out, d)
		}
	}
	return out
}

func (t *tree) device(kind observatory.Kind, name string) (*device, bool) {
	for _, d := range t.devices {
		if d.kind == kind && d.name() == name {
			return d, true
		}
	}
	return nil, false
}

func (t *tree) deviceByKey(key string) (*device, bool) {
	for _, d := range t.devices {
		if d.key() == key {
			return d, true
		}
	}
	return nil, false
}

// missingParent returns the first resource above a device that does
// not exist, such as "OpticalTrain east-imaging". It returns "" when
// every parent up to the Observatory exists, and when the device names
// no parent.
func (t *tree) missingParent(d *device) string {
	p, installed := d.declaredParent()
	switch {
	case !installed:
		return ""
	case p.Kind == observatory.OpticalTrainKind:
		train, ok := t.trainAsWritten(p.Name)
		if !ok {
			return "OpticalTrain " + p.Name
		}
		return t.missingTelescope(train.Spec.Telescope)
	case p.Kind == observatory.TelescopeKind:
		return t.missingTelescope(p.Name)
	}
	return t.missingObservatory(p.Name)
}

func (t *tree) missingTelescope(name string) string {
	telescope, ok := t.telescopes[name]
	if !ok {
		return "Telescope " + name
	}
	return t.missingObservatory(telescope.Spec.Observatory)
}

func (t *tree) missingObservatory(name string) string {
	if _, ok := t.observatories[name]; !ok {
		return "Observatory " + name
	}
	return ""
}

// asJSON converts a value to the generic form of a decoded JSON
// document, so two statuses compare equal when the API server would
// store the same document. A number from a watch cache is an int64, and
// the same number built by the operator is a float64. After the
// conversion both are float64.
func asJSON(v any) any {
	body, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	_ = json.Unmarshal(body, &out)
	return out
}
