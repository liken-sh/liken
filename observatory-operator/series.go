package main

// The domain series of one namespace (metrics.go). The status writer
// sets the series of each device, telescope, and guider from what it
// read to compose their statuses, once per statusWindow. The runners
// count the failed steps and time each activation, and the connections
// to the INDI servers and to PHD2 set the observation series from their
// events.
//
// A label names a resource that a person declared, a kind, a step, a
// phase, or a state. A person creates a reservation for each night, so
// no series names one: the series of a reservation name its telescope.
// The status writer deletes the gauges of each resource that is gone
// from the tree, and the operator of a namespace deletes every gauge of
// the namespace when it stops. A counter stays, because the failures
// and the disconnects that it counted still happened.

import (
	"slices"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// telescopePhases are the values of telescope_phase's phase label.
var telescopePhases = []observatory.Phase{
	observatory.PhaseIdle, observatory.PhaseActivating, observatory.PhaseReady,
	observatory.PhaseDeactivating, observatory.PhaseError,
}

// guiderStates are the values of guider_state's state label: each
// AppState of PHD2's event server.
var guiderStates = []observatory.GuiderState{
	observatory.GuiderStopped, observatory.GuiderSelected, observatory.GuiderCalibrating,
	observatory.GuiderGuiding, observatory.GuiderLostLock, observatory.GuiderPaused,
	observatory.GuiderLooping,
}

// namespaceSeries sets the series of one namespace.
type namespaceSeries struct {
	m         *metrics
	namespace string

	mu sync.Mutex
	// connected holds the last observed connection of each device, by
	// its key, to count a drop. It is a cache of the gauge's value,
	// because a GaugeVec cannot answer the value it holds.
	connected map[string]bool
	// present holds the series of each resource that the last pass set,
	// so the next pass deletes the series of a resource that is gone.
	present map[seriesKey]bool
}

// seriesKey names one resource among the series: a device by its kind
// and name, or a Telescope or a Guider.
type seriesKey struct {
	kind observatory.Kind
	name string
}

func (m *metrics) namespace(namespace string) *namespaceSeries {
	return &namespaceSeries{m: m, namespace: namespace, connected: map[string]bool{}, present: map[seriesKey]bool{}}
}

// deviceReading is what the status writer read of one device.
type deviceReading struct {
	// observed is true when the operator knows whether the device's
	// driver is connected: the device has no pod, the operator refuses
	// it, or the operator has a connection to its server. A device whose
	// server does not answer is not observed, and its gauges keep their
	// last values, as root plan 65 states: observation_valid says how
	// old they are.
	observed  bool
	connected bool
	// claimed is true while the record of a session names the device.
	claimed bool
	// leaving is true while the operator disconnects the device on
	// purpose: a release, or a delete.
	leaving bool
	// standard holds the device's typed status fields, such as a
	// *observatory.CameraStandard.
	standard any
}

// device sets the series of one device. A drop of the connection
// counts while a session holds the device, except while the operator
// itself disconnects it. The status writer reads once per statusWindow,
// so a drop and a reconnect within one window do not count.
func (s *namespaceSeries) device(d *device, r deviceReading) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := seriesKey{d.kind, d.name()}
	labels := []string{s.namespace, d.kind.Name, d.name()}
	if !s.present[key] {
		s.m.deviceDisconnects.WithLabelValues(labels...)
	}
	s.present[key] = true
	s.m.deviceClaimed.WithLabelValues(labels...).Set(gaugeValue(r.claimed))
	if !r.observed {
		return
	}
	if was := s.connected[d.key()]; was && !r.connected && r.claimed && !r.leaving {
		s.m.deviceDisconnects.WithLabelValues(labels...).Inc()
	}
	s.connected[d.key()] = r.connected
	s.m.deviceConnected.WithLabelValues(labels...).Set(gaugeValue(r.connected))
	if d.kind == observatory.CameraKind {
		s.camera(d.name(), r.standard)
	}
}

// camera sets a camera's temperature and cooler power, and deletes
// each one that its driver does not report.
func (s *namespaceSeries) camera(name string, standard any) {
	camera, _ := standard.(*observatory.CameraStandard)
	var temperature, power *float64
	if camera != nil && camera.Temperature != nil {
		temperature = camera.Temperature.Value
	}
	if camera != nil && camera.CoolerPower != nil && camera.CoolerPower.Value != nil {
		// CCD_COOLER_POWER is a percentage, and the ratio is the unit of
		// root plan 65's naming rules.
		ratio := *camera.CoolerPower.Value / 100
		power = &ratio
	}
	setOrDelete(s.m.cameraTemperature, temperature, s.namespace, name)
	setOrDelete(s.m.cameraCoolerPower, power, s.namespace, name)
}

// telescope sets the phase of one telescope, and publishes its step
// failures at zero.
func (s *namespaceSeries) telescope(name string, phase observatory.Phase) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := seriesKey{observatory.TelescopeKind, name}
	if !s.present[key] {
		for _, step := range slices.Concat(observatory.ActivationSteps, observatory.DeactivationSteps) {
			s.m.stepFailures.WithLabelValues(s.namespace, name, string(step))
		}
	}
	s.present[key] = true
	for _, p := range telescopePhases {
		s.m.telescopePhase.WithLabelValues(s.namespace, name, string(p)).Set(gaugeValue(p == phase))
	}
}

// guiderReading is what the status writer read of one guider's PHD2.
type guiderReading struct {
	// running is true while the guider has a pod, and the operator a
	// connection to its PHD2.
	running bool
	// open is true while that connection is open.
	open  bool
	state observatory.GuiderState
	rms   *observatory.GuiderRMS
}

// guiderReading answers what the series of a guider need, from its
// composed status and its connection to PHD2. The status holds PHD2's
// state and its RMS error in arc-seconds only while the connection is
// open (guiderReadings).
func (o *operator) guiderReading(guider *observatory.Guider, status observatory.GuiderStatus) guiderReading {
	conn, ok := o.guiderConns.get(guiderName(guider))
	if !ok {
		return guiderReading{}
	}
	return guiderReading{running: true, open: conn.client.State().Open, state: status.State, rms: status.RMS}
}

// guider sets the state and the guiding error of one guider. A guider
// with no pod runs no PHD2, so its series are deleted. A connection
// that is not open keeps the series at their last values, as a device
// whose server does not answer does.
func (s *namespaceSeries) guider(name string, r guiderReading) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.present[seriesKey{observatory.GuiderKind, name}] = true
	switch {
	case !r.running:
		s.forgetGuider(name)
	case r.open:
		for _, state := range guiderStates {
			s.m.guiderState.WithLabelValues(s.namespace, name, string(state)).Set(gaugeValue(state == r.state))
		}
		var ra, dec, total *float64
		if r.rms != nil {
			ra, dec, total = &r.rms.RA, &r.rms.Dec, &r.rms.Total
		}
		setOrDelete(s.m.guidingError, ra, s.namespace, name, "ra")
		setOrDelete(s.m.guidingError, dec, s.namespace, name, "dec")
		setOrDelete(s.m.guidingError, total, s.namespace, name, "total")
	}
}

func (s *namespaceSeries) forgetGuider(name string) {
	match := prometheus.Labels{"namespace": s.namespace, "guider": name}
	s.m.guiderState.DeletePartialMatch(match)
	s.m.guidingError.DeletePartialMatch(match)
}

// hasSeries reports whether the tree holds the resource of a key.
func (t *tree) hasSeries(key seriesKey) bool {
	switch key.kind {
	case observatory.TelescopeKind:
		_, ok := t.telescopes[key.name]
		return ok
	case observatory.GuiderKind:
		_, ok := t.guiders[key.name]
		return ok
	}
	_, ok := t.deviceByKey(key.kind.Name + "/" + key.name)
	return ok
}

// endPass deletes the gauges of each resource that present does not
// hold: one that a person deleted.
func (s *namespaceSeries) endPass(present func(seriesKey) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key := range s.present {
		if present(key) {
			continue
		}
		delete(s.present, key)
		switch key.kind {
		case observatory.TelescopeKind:
			s.m.telescopePhase.DeletePartialMatch(prometheus.Labels{"namespace": s.namespace, "telescope": key.name})
		case observatory.GuiderKind:
			s.forgetGuider(key.name)
		default:
			delete(s.connected, key.kind.Name+"/"+key.name)
			match := prometheus.Labels{"namespace": s.namespace, "kind": key.kind.Name, "name": key.name}
			s.m.deviceConnected.DeletePartialMatch(match)
			s.m.deviceClaimed.DeletePartialMatch(match)
			if key.kind == observatory.CameraKind {
				s.camera(key.name, nil)
			}
		}
	}
}

// forget deletes every gauge of the namespace, when its operator stops.
func (s *namespaceSeries) forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	match := prometheus.Labels{"namespace": s.namespace}
	for _, gauges := range []*prometheus.GaugeVec{
		s.m.deviceConnected, s.m.deviceClaimed, s.m.observationValid, s.m.observationSuccess,
		s.m.telescopePhase, s.m.cameraTemperature, s.m.cameraCoolerPower, s.m.guidingError, s.m.guiderState,
	} {
		gauges.DeletePartialMatch(match)
	}
	s.connected, s.present = map[string]bool{}, map[seriesKey]bool{}
}

// stepFailed counts one failed step of a telescope's reservation.
func (s *namespaceSeries) stepFailed(telescope string, step observatory.StepName) {
	s.m.stepFailures.WithLabelValues(s.namespace, telescope, string(step)).Inc()
}

// activated records how long one activation of a telescope took.
func (s *namespaceSeries) activated(telescope string, took time.Duration) {
	s.m.activationDuration.WithLabelValues(s.namespace, telescope).Observe(took.Seconds())
}

// observation records the state of the connection to one source, the
// INDI server or the PHD2 of a resource, such as Telescope/east. A
// message on an open connection is a success, and moves the
// timestamp.
func (s *namespaceSeries) observation(source string, open bool) {
	s.m.observationValid.WithLabelValues(s.namespace, source).Set(gaugeValue(open))
	if open {
		s.m.observationSuccess.WithLabelValues(s.namespace, source).Set(float64(time.Now().UnixNano()) / 1e9)
	}
}

// forgetSource deletes the series of a source whose connection the
// operator closed, because the source's pod is gone.
func (s *namespaceSeries) forgetSource(source string) {
	s.m.observationValid.DeleteLabelValues(s.namespace, source)
	s.m.observationSuccess.DeleteLabelValues(s.namespace, source)
}

// setOrDelete sets a gauge to a value, or deletes it when the value is
// absent.
func setOrDelete(gauges *prometheus.GaugeVec, value *float64, labels ...string) {
	if value == nil {
		gauges.DeleteLabelValues(labels...)
		return
	}
	gauges.WithLabelValues(labels...).Set(*value)
}

// gaugeValue answers 1 for true and 0 for false.
func gaugeValue(state bool) float64 {
	if state {
		return 1
	}
	return 0
}

// sourceOf names the source of an object that the operator created:
// the kind and the name of the resource that caused it, such as
// Telescope/east.
func sourceOf(p *pod) string {
	return p.Metadata.Labels[labelKind] + "/" + p.Metadata.Labels[labelResource]
}
