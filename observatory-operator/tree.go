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
	// homes holds the server of each device pod, computed once from the
	// pods (homeOf).
	homesOnce sync.Once
	homes     map[podKey]string
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
// on (podOf): the server in the pod's label, which holds the pod until
// it is gone (heldElsewhere). A served device has its owner's pod while
// it shares the owner's INDI device. The pod places a device that
// leaves the tree of its parents, because a person moved or deleted the
// device or the parent that places it, and the finalizer keeps a
// deleted device's object until its deactivation ran through that
// driver. A device whose finalizer a person removed by hand is placed
// the same way, because its pod still runs its driver.
func (t *tree) runningOn(d *device) (serverRef, bool) {
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

// releasedOn answers the devices that the release of one server acts
// on, in the order of t.devices: the devices that the tree places there
// (devicesOn), and the devices that only the pod of their driver places
// there (devicesRunningOn), because a person deleted them or their
// train.
func (t *tree) releasedOn(s serverRef) []*device {
	placed := map[*device]bool{}
	for _, d := range t.devicesOn(s) {
		placed[d] = true
	}
	var out []*device
	for _, d := range t.devices {
		if on, running := t.runningOn(d); placed[d] || (running && on == s) {
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
// the tree refuses (served.go), and a device whose pod another server
// still holds (heldElsewhere).
func (t *tree) devicesOn(s serverRef) []*device {
	var out []*device
	server := s.String()
	for _, d := range t.placedOn(s) {
		if _, owned := t.ownerOf(d); !owned || t.heldElsewhere(d, server) {
			continue
		}
		if _, taken := t.driverTakenBy(d); !taken {
			out = append(out, d)
		}
	}
	return out
}

// heldElsewhere reports whether the pod of a device's driver (podOf)
// belongs to a server other than the named one, and that server's pod
// runs. A device pod belongs to the server in its label until the pod
// is gone, because socat in the pod serves one connection, and the
// server that holds it runs the device's deactivation, switches its
// output off, and stops its driver before the pod goes (moves.go,
// deactivation.go). So a device that a person moves from one running
// server to another stays on the server it left until its pod is gone,
// and then joins the other server in a pod of its own. A pod whose
// server no longer runs belongs to no server, and the server that the
// tree places the device on replaces it (ensure).
func (t *tree) heldElsewhere(d *device, server string) bool {
	runner, ok := t.runnerOf(d)
	if !ok || (!d.runsDriver() && !t.shares(d)) {
		return false
	}
	on, ok := t.homeOf(runner)
	if !ok || on == server {
		return false
	}
	_, running := t.pods[on]
	return running
}

// podKey names the resource of a device pod by the pod's labels.
type podKey struct{ kind, resource string }

// homeOf answers the server in the label of the pod of a device that
// runs a driver, while the pod exists. The status writer reads the
// tree once for each reading, so the tree indexes the pods once by
// their labels, and a lookup builds no name.
func (t *tree) homeOf(runner *device) (string, bool) {
	t.homesOnce.Do(func() {
		t.homes = map[podKey]string{}
		for _, p := range t.pods {
			if l := p.Metadata.Labels; l[labelRole] == roleDevice {
				t.homes[podKey{l[labelKind], l[labelResource]}] = l[labelServer]
			}
		}
	})
	on, ok := t.homes[podKey{runner.kind.Name, runner.name()}]
	return on, ok
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
