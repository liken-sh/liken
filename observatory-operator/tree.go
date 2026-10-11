package main

// The tree is one reading of the observatory's resources and of the
// objects the operator created, taken from the watches' stores. Each
// resource names its parent in its spec, so every reference points up
// the tree, and the tree answers the questions that point down: which
// devices a telescope's server runs, and which trains a tube serves.

import (
	"encoding/json"
	"slices"
	"sync"

	"github.com/liken-sh/liken/kubernetes/memo"
	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// deviceObject is one device resource of any of the 13 device kinds.
// The spec holds the fields that every device kind has, so one type
// reads them all, and the status holds the typed fields, the readings,
// and the display of the kind as values of any type. The CRD of each kind refuses the fields it does not have, so a
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
	observatory.Procedures[action]
	// raw is the spec as the API server holds it. Each kind's settings
	// differ, so the Configure step decodes them from it into the
	// kind's own type (settings.go).
	raw []byte
}

// UnmarshalJSON reads the fields that every kind shares, and keeps the
// whole spec for the settings of the kind.
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
	// Standard holds the typed fields of the kind's standard
	// properties, such as a *observatory.CameraStandard, which the
	// JSON writes at the level of the status, as each kind's status
	// embeds it. The cached copy of a device holds none (watch.go).
	Standard any `json:"-"`
	Readings any `json:"readings,omitempty"`
	Display  any `json:"display,omitempty"`
}

// MarshalJSON writes the typed fields beside the rest of the status, as
// encoding/json writes an embedded struct.
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

// String answers the name of the server's pod and Service, which is
// also its key among the operator's INDI connections.
func (s serverRef) String() string { return generatedName(s.kind, s.name) }

// tree is one copy of every watched object. The goroutines that read
// between two changes share one tree (operator.snapshot), so no code
// changes a tree or an object in it. Code that changes an object
// changes its own copy.
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
	devices    []*device
	pods       map[string]*pod
	services   map[string]*service
	configMaps map[string]*configMap
	jobs       map[string]*job
	claims     map[string]bool

	// taken is computed once from the devices (shareddrivers.go).
	takenOnce sync.Once
	taken     map[*device]*device
	// served holds the owner of each served device (served.go).
	served servedVerdicts
}

// parent answers the resource above a device, from the parent field
// its kind has. The answer is false for a device on the shelf, which
// names no parent.
func (d *device) parent() (observatory.Parent, bool) {
	s := d.object.Spec
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

// server answers the INDI server that runs a device: its telescope's,
// or its observatory's. A device on a train runs on the server of the
// train's telescope. The answer is false for a device on the shelf,
// while a train that the device names is missing, and for a device
// that a person deleted. The finalizer keeps a deleted device's object
// until its server let it go, and a server that runs it treats it as a
// device that left (finalizers.go).
func (t *tree) server(d *device) (serverRef, bool) {
	p, installed := d.parent()
	switch {
	case !installed, deleting(d.object.Metadata):
		return serverRef{}, false
	case p.Kind == observatory.OpticalTrainKind:
		train, ok := t.trains[p.Name]
		if !ok {
			return serverRef{}, false
		}
		return serverRef{observatory.TelescopeKind, train.Spec.Telescope}, true
	case p.Kind == observatory.TelescopeKind:
		return serverRef{observatory.TelescopeKind, p.Name}, true
	}
	return serverRef{observatory.ObservatoryKind, p.Name}, true
}

// runningOn answers the server that the pod of a device's driver runs
// on, while the device holds the finalizer and has the pod (podOf): a
// served device has its owner's pod while it holds the finalizer. The
// pod places a device that leaves the tree of its parents, because a
// person deleted the device or the parent that places it, and the
// finalizer keeps the device's object until its deactivation ran
// through that driver.
func (t *tree) runningOn(d *device) (serverRef, bool) {
	if !holdsFinalizer(d.object.Metadata) {
		return serverRef{}, false
	}
	_, p, running := t.podOf(d)
	if !running {
		return serverRef{}, false
	}
	return t.serverNamed(p.Metadata.Labels[labelServer])
}

// devicesRunningOn answers the devices whose driver's pod runs on one
// server (runningOn), in the order of t.devices, whatever their parents
// say: the devices whose deactivation that server's driver can run.
func (t *tree) devicesRunningOn(s serverRef) []*device {
	var out []*device
	for _, d := range t.devices {
		if on, ok := t.runningOn(d); ok && on == s {
			out = append(out, d)
		}
	}
	return out
}

// serverNamed answers the server of a Telescope or an Observatory in
// the tree by the name of its pod.
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

// devicesOn answers the devices that one server runs, in the order of
// t.devices: the devices that run a driver, and the devices that one
// of them serves. A device whose driver another device on the server
// runs is left out (shareddrivers.go), and so is a served device that
// the tree refuses (served.go).
func (t *tree) devicesOn(s serverRef) []*device {
	var out []*device
	for _, d := range t.placedOn(s) {
		if _, owned := t.ownerOf(d); !owned {
			continue
		}
		if _, taken := t.driverTakenBy(d); !taken {
			out = append(out, d)
		}
	}
	return out
}

// placedOn answers the devices whose parents place them on one server,
// in the order of t.devices.
func (t *tree) placedOn(s serverRef) []*device {
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

// deviceByKey answers the device of a key, such as Camera/east-main.
func (t *tree) deviceByKey(key string) (*device, bool) {
	for _, d := range t.devices {
		if d.key() == key {
			return d, true
		}
	}
	return nil, false
}

// missingParent answers the first resource up the tree from a device
// that does not exist, as "OpticalTrain east-imaging", or "" when
// every parent up to the Observatory exists, or when the device is on
// the shelf.
func (t *tree) missingParent(d *device) string {
	p, installed := d.parent()
	switch {
	case !installed:
		return ""
	case p.Kind == observatory.OpticalTrainKind:
		train, ok := t.trains[p.Name]
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
