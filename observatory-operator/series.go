package main

// The Prometheus series of one namespace, which metrics.go registers.
// Once per statusWindow, the status writer sets the gauges of each
// device, telescope, and guider from the readings it composes their
// statuses from. The reservation runners count failed steps and time
// each activation, and the connections to the INDI servers and to PHD2
// set the observation series as they receive events.
//
// A new Reservation is created every night, so no series has a
// reservation label, and a reservation's series have the label of its telescope's
// name. The status writer deletes the gauges of a resource that a
// person deleted, and the operator of a namespace deletes all of the
// namespace's gauges when it stops. Counters are never deleted,
// because the failures and disconnects they counted still happened.

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

// namespaceSeries sets and deletes the series of one namespace.
type namespaceSeries struct {
	m         *metrics
	namespace string

	mu sync.Mutex
	// connected has the last observed connection of each device, by its
	// key, so that device can count a drop. It copies the gauge's value
	// because a GaugeVec cannot be read back.
	connected map[string]bool
	// present has the resources whose series the status writer set, so
	// that endPass can delete the series of one that left the tree.
	present map[seriesKey]bool
}

// seriesKey identifies one resource that has series: a device by its
// kind and name, a Telescope, or a Guider.
type seriesKey struct {
	kind observatory.Kind
	name string
}

func (m *metrics) namespace(namespace string) *namespaceSeries {
	return &namespaceSeries{m: m, namespace: namespace, connected: map[string]bool{}, present: map[seriesKey]bool{}}
}

type deviceReading struct {
	// observed is true when the status writer could read whether the
	// driver is connected: the device has no pod, the operator refuses
	// it, or the operator's connection to its INDI server is open.
	// Otherwise the device's gauges keep their last values, and
	// observation_valid and observation_last_success_timestamp_seconds
	// show that they are stale and how old they are.
	observed  bool
	connected bool
	// claimed is true while a session's record lists the device.
	claimed bool
	// leaving is true while the operator disconnects the device on
	// purpose, for a release or a delete.
	leaving bool
	// standard is the device's typed status, such as a
	// *observatory.CameraStandard.
	standard any
}

// device sets the series of one device. A lost connection counts as a
// drop only while a session claims the device and the operator is not
// disconnecting it itself. The status writer reads once per
// statusWindow, so a drop and a reconnect within one window are not
// counted.
func (s *namespaceSeries) device(d *device, r deviceReading) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := seriesKey{d.kind, d.name()}
	labels := []string{s.namespace, d.kind.Name, d.name()}
	// Creating the counter makes it appear in scrapes at zero before the
	// first drop.
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

// camera sets a camera's temperature and cooler power gauges, and
// deletes each gauge whose value the driver does not report.
func (s *namespaceSeries) camera(name string, standard any) {
	camera, _ := standard.(*observatory.CameraStandard)
	var temperature, power *float64
	if camera != nil && camera.Temperature != nil {
		temperature = camera.Temperature.Value
	}
	if camera != nil && camera.CoolerPower != nil && camera.CoolerPower.Value != nil {
		// CCD_COOLER_POWER is a percentage. Prometheus naming conventions
		// use a ratio from 0 to 1 for such a value.
		ratio := *camera.CoolerPower.Value / 100
		power = &ratio
	}
	setOrDelete(s.m.cameraTemperature, temperature, s.namespace, name)
	setOrDelete(s.m.cameraCoolerPower, power, s.namespace, name)
}

// telescope sets the phase gauges of one telescope. On the first call
// for a telescope, it also creates the step failure counters, so they
// appear in scrapes at zero before the first failure.
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

type guiderReading struct {
	// running is true while the operator has a connection to the
	// guider's PHD2, which it keeps for as long as the guider has a pod.
	running bool
	open    bool
	state   observatory.GuiderState
	rms     *observatory.GuiderRMS
}

// guiderReading takes PHD2's state and RMS error from the guider's
// composed status, which has them only while the connection is open
// (guiderReadings).
func (o *operator) guiderReading(guider *observatory.Guider, status observatory.GuiderStatus) guiderReading {
	conn, ok := o.guiderConns.get(guiderName(guider))
	if !ok {
		return guiderReading{}
	}
	return guiderReading{running: true, open: conn.client.State().Open, state: status.State, rms: status.RMS}
}

// guider sets the state and guiding error gauges of one guider. A
// guider with no pod runs no PHD2, so its series are deleted. While the
// connection is not open, the gauges keep their last values, as the
// gauges of a device whose server does not respond do.
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

// hasSeries reports whether the tree contains the resource that key
// identifies.
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

// endPass deletes the gauges of each resource for which present
// returns false, because a person deleted it.
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

// forget deletes every gauge of the namespace when its operator stops.
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

func (s *namespaceSeries) stepFailed(telescope string, step observatory.StepName) {
	s.m.stepFailures.WithLabelValues(s.namespace, telescope, string(step)).Inc()
}

func (s *namespaceSeries) activated(telescope string, took time.Duration) {
	s.m.activationDuration.WithLabelValues(s.namespace, telescope).Observe(took.Seconds())
}

// observation records whether the connection to one source is open:
// the INDI server or the PHD2 of a resource, such as Telescope/east.
// Each call while the connection is open updates the last success
// timestamp.
func (s *namespaceSeries) observation(source string, open bool) {
	s.m.observationValid.WithLabelValues(s.namespace, source).Set(gaugeValue(open))
	if open {
		s.m.observationSuccess.WithLabelValues(s.namespace, source).Set(float64(time.Now().UnixNano()) / 1e9)
	}
}

// forgetSource deletes the series of a source whose pod is gone and
// whose connection the operator closed.
func (s *namespaceSeries) forgetSource(source string) {
	s.m.observationValid.DeleteLabelValues(s.namespace, source)
	s.m.observationSuccess.DeleteLabelValues(s.namespace, source)
}

func setOrDelete(gauges *prometheus.GaugeVec, value *float64, labels ...string) {
	if value == nil {
		gauges.DeleteLabelValues(labels...)
		return
	}
	gauges.WithLabelValues(labels...).Set(*value)
}

func gaugeValue(state bool) float64 {
	if state {
		return 1
	}
	return 0
}

// sourceOf returns the source label for a pod that the operator
// created: the kind and name of the resource that the pod is for, such
// as Telescope/east.
func sourceOf(p *pod) string {
	return p.Metadata.Labels[labelKind] + "/" + p.Metadata.Labels[labelResource]
}
