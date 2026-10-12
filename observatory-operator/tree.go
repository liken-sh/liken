package main

// The tree is one reading of the observatory's resources and of the
// objects the operator created, taken from the watches' stores. Each
// resource names its parent in its spec, so every reference points up
// the tree, and the tree answers the questions that point down: which
// devices a telescope's server runs, and which trains a tube serves.

import (
	"encoding/json"
	"slices"
	"strings"
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

// device is one device resource and its kind. While a session holds
// the device, its spec holds what the session recorded (records.go).
type device struct {
	kind   observatory.Kind
	object deviceObject
	// declared is the spec as the API server holds it, while a record
	// replaced it, and nil otherwise (declaredSpec).
	declared *deviceSpec
	// record is the device's record, and recordedOn the held server
	// whose record names it, while a session holds the device.
	record     *observatory.SessionDevice
	recordedOn serverRef
}

// declaredSpec answers the spec as a person wrote it.
func (d *device) declaredSpec() *deviceSpec {
	if d.declared != nil {
		return d.declared
	}
	return &d.object.Spec
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
	// held records what the sessions hold, computed once (sessions).
	heldOnce sync.Once
	held     sessions
	// served holds the owner of each served device (served.go).
	served servedVerdicts
	// records holds the record of each held server that a holder's
	// status records, and recordedTubes the spec of each tube that one
	// of them names (records.go).
	records       map[serverRef]observatory.Session
	recordedTubes map[string]observatory.OpticalTubeSpec
}

// parent answers the resource above a device, from the parent field
// its kind has. The answer is false for a device on the shelf, which
// names no parent.
func (d *device) parent() (observatory.Parent, bool) { return parentOf(&d.object.Spec) }

// declaredParent answers the parent that the device's spec names as a
// person wrote it (declaredSpec).
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

// declared answers the INDI server that a device's parents place it
// on: its telescope's, or its observatory's. A device on a train runs
// on the server of the train's telescope. The answer is false for a
// device on the shelf, while a train that the device names is missing,
// and for a device that a person deleted. The finalizer keeps a deleted
// device's object until its server let it go, and a server that runs
// it treats it as a device that left (finalizers.go).
func (t *tree) declared(d *device) (serverRef, bool) {
	p, installed := d.declaredParent()
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

// server answers the INDI server that runs a device. While a session
// holds a server and its pod runs, the server runs a fixed set of
// devices: the devices that it started, which its pod lists in its
// drivers annotation when it is created and whose pods carry its label
// (heldOn). An eviction can delete the server's pod, or every pod of
// the server. Then the record in the status of the reservations that
// hold the server names the set, and the runner creates the pods again
// with it (records.go). A server whose holders recorded nothing yet
// takes the devices that the specs place there, as a new session
// does. During the session, the pods and the record place such a
// device, and its spec does not, so a spec edit that would move it to
// another telescope, train, or the shelf, or a train deleted under it, takes
// effect when the session ends, and the release stops the device as
// its own. A device that its parents place on such a server and that
// the server did not start waits for the next session. So a device
// runs on one server at a time: a move between two running servers
// would need one server to release the device while the other starts
// it, and the steps of both sessions to agree on where it is. A deleted
// device leaves its server at once, because its finalizer holds it
// only until its deactivation ran.
func (t *tree) server(d *device) (serverRef, bool) {
	if deleting(d.object.Metadata) {
		return serverRef{}, false
	}
	if on, ok := t.heldOn(d); ok {
		return on, true
	}
	ref, ok := t.declared(d)
	if ok && t.started(ref) {
		return serverRef{}, false
	}
	return ref, ok
}

// sessions is what the reservations hold in one tree. The status
// writer places every device on each pass, so a tree computes it once,
// and a lookup builds no pod name.
type sessions struct {
	// holders names, for each held server, the reservations that hold
	// it, sorted: one for a telescope's server, and one for each held
	// telescope of an observatory for the observatory's server.
	holders map[serverRef][]string
	// started holds each held server whose set of devices is fixed: a
	// holder's status records the set, or its pod or the pod of one of
	// its devices exists.
	started map[serverRef]bool
	// runs gives the started server whose set holds each device that
	// runs a driver: the server in the label of the device's pod, the
	// server whose pod lists the device's driver, or, while that pod is
	// gone, the server that lostFrom answers.
	runs map[*device]serverRef
	// podded holds each device that runs a driver and whose pod exists.
	podded map[*device]bool
}

// sessions answers what the reservations hold, from their steps
// (holds) and the pods.
func (t *tree) sessions() sessions {
	t.heldOnce.Do(func() {
		t.held = sessions{holders: map[serverRef][]string{}, started: map[serverRef]bool{},
			runs: map[*device]serverRef{}, podded: map[*device]bool{}}
		for _, name := range sortedNames(t.reservations) {
			res := t.reservations[name]
			if !holds(res) {
				continue
			}
			scope := serverRef{observatory.TelescopeKind, res.Spec.Telescope}
			t.held.holders[scope] = append(t.held.holders[scope], name)
			if telescope, ok := t.telescopes[scope.name]; ok {
				site := serverRef{observatory.ObservatoryKind, telescope.Spec.Observatory}
				t.held.holders[site] = append(t.held.holders[site], name)
			}
		}
		byServer, byPod := map[string]serverRef{}, map[string]serverRef{}
		for ref := range t.held.holders {
			byServer[ref.String()] = ref
			if p, ok := t.pods[ref.String()]; ok {
				t.held.started[ref] = true
				for _, address := range strings.Fields(p.Metadata.Annotations[annotationDrivers]) {
					name, _, _ := strings.Cut(address, ":")
					byPod[name] = ref
				}
			}
		}
		for name, p := range t.pods {
			if ref, held := byServer[p.Metadata.Labels[labelServer]]; held && p.Metadata.Labels[labelRole] == roleDevice {
				byPod[name] = ref
				t.held.started[ref] = true
			}
		}
		for ref := range t.held.holders {
			if _, recorded := t.records[ref]; recorded {
				t.held.started[ref] = true
			}
		}
		for _, d := range driverDevices(t.devices) {
			name, err := objectName(d.kind, d.name())
			if err != nil {
				continue
			}
			_, t.held.podded[d] = t.pods[name]
			if ref, ok := byPod[name]; ok {
				t.held.runs[d] = ref
			} else if ref, ok := t.lostFrom(d); ok {
				t.held.runs[d] = ref
			}
		}
	})
	return t.held
}

// lostFrom answers the started server whose set holds a device whose
// pod is gone, while that server's pod is gone too: the server whose
// record names the device.
func (t *tree) lostFrom(d *device) (serverRef, bool) {
	if d.record == nil {
		return serverRef{}, false
	}
	_, running := t.pods[d.recordedOn.String()]
	return d.recordedOn, t.held.started[d.recordedOn] && !running
}

// started reports whether a session holds a server and the server's
// set of devices is fixed (heldOn).
func (t *tree) started(ref serverRef) bool {
	return t.sessions().started[ref]
}

// heldOn answers the held server whose set of devices holds a device. A
// served device belongs to the set of its owner's server once it holds
// the finalizer, which it gets just before its owner's pod is created
// (startDevices). Before that, a served device that its parents place
// with its owner joins the set with the owner's pod.
func (t *tree) heldOn(d *device) (serverRef, bool) {
	runner, ok := t.runnerOf(d)
	if !ok {
		return serverRef{}, false
	}
	held := t.sessions()
	on, ok := held.runs[runner]
	if !ok || d.runsDriver() || holdsFinalizer(d.object.Metadata) {
		return on, ok
	}
	declared, placed := t.declared(d)
	return on, !held.podded[runner] && placed && declared == on
}

// runningOn answers the server that the pod of a device's driver runs
// on (podOf): the server in the pod's label. A served device has its
// owner's pod while it shares the owner's INDI device. The pod places
// a device that a person deleted, or whose finalizer a person removed
// by hand, while its deactivation runs through that driver.
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
// on, in the order of t.devices: the devices whose driver's pod runs
// there, and the devices that the server's session recorded, which
// include the devices whose pods the release deleted already. A device
// that its parents place there and that the session never started is
// not one of them.
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

// placedOn answers the devices that one server runs or will run
// (server), in the order of t.devices.
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
	p, installed := d.declaredParent()
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
