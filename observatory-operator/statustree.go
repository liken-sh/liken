package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

func found() observatory.Condition {
	return condition(observatory.ConditionParentFound, observatory.ConditionTrue, "Found", "Found every parent")
}

func missing(what string) observatory.Condition {
	return condition(observatory.ConditionParentFound, observatory.ConditionFalse, "ParentMissing", "Missing "+what)
}

func parentCondition(missingParent string) observatory.Condition {
	if missingParent != "" {
		return missing(missingParent)
	}
	return found()
}

func readyCondition(phase observatory.Phase, message string) observatory.Condition {
	status := observatory.ConditionFalse
	if phase == observatory.PhaseReady {
		status = observatory.ConditionTrue
	}
	return condition(observatory.ConditionReady, status, string(phase), message)
}

func withGeneration(conditions []observatory.Condition, generation int64) []observatory.Condition {
	for i := range conditions {
		conditions[i].ObservedGeneration = generation
	}
	return conditions
}

// serverStatus returns a server's endpoint, pod, and node, or nil when
// its pod does not exist.
func (o *operator) serverStatus(t *tree, ref serverRef) *observatory.Server {
	p, ok := t.pods[ref.String()]
	if !ok {
		return nil
	}
	return &observatory.Server{
		Endpoint: observatory.Endpoint{Service: ref.String(), Host: serviceHost(ref.String(), o.namespace), Port: serverPort},
		Pod:      p.Metadata.Name,
		Node:     p.Spec.NodeName,
	}
}

func refs(devices []*device, composed map[string]deviceStatus) []observatory.DeviceRef {
	var out []observatory.DeviceRef
	for _, d := range devices {
		out = append(out, observatory.DeviceRef{Kind: d.kind.Name, Name: d.name(), Phase: composed[d.key()].Phase})
	}
	return out
}

// directDevices returns the devices whose spec names this resource as
// their parent. It leaves out the devices on the resource's optical
// trains.
func directDevices(t *tree, kind observatory.Kind, name string) []*device {
	var out []*device
	for _, d := range t.devices {
		if p, installed := d.parent(); installed && p.Kind == kind && p.Name == name {
			out = append(out, d)
		}
	}
	return out
}

// telescopePhase returns a telescope's phase and the reservation that
// holds it. The phase is Idle when no reservation holds the telescope.
func (o *operator) telescopePhase(t *tree, telescope string) (observatory.Phase, *observatory.Reservation) {
	holder, ok := o.claims.holderOf(telescope)
	if !ok {
		return observatory.PhaseIdle, nil
	}
	r, ok := t.reservations[holder]
	if !ok {
		return observatory.PhaseIdle, nil
	}
	switch r.Status.Phase {
	case observatory.ReservationReady:
		return observatory.PhaseReady, r
	case observatory.ReservationDeactivating, observatory.ReservationReleased:
		// A Released reservation has finished every step, and its
		// runner releases the telescope after the status write, so the
		// telescope is leaving and no longer in use.
		return observatory.PhaseDeactivating, r
	case observatory.ReservationFailed:
		return observatory.PhaseError, r
	}
	return observatory.PhaseActivating, r
}

// useOfServer returns a server's use from the phases of the
// telescopes that use it: maintained when any is Ready, activating when any
// is Activating, releasing when any is Deactivating, and idle
// otherwise. An observatory's server is used by every telescope in the
// observatory, because the runner of each Ready telescope keeps the
// observatory's pods (steady.go), and the StartSite step of each
// activation starts them.
func (o *operator) useOfServer(t *tree, ref serverRef) serverUse {
	telescopes := []string{ref.name}
	if ref.kind == observatory.ObservatoryKind {
		telescopes = nil
		for _, name := range sortedNames(t.telescopes) {
			if t.telescopes[name].Spec.Observatory == ref.name {
				telescopes = append(telescopes, name)
			}
		}
	}
	best := idle
	for _, name := range telescopes {
		switch phase, _ := o.telescopePhase(t, name); phase {
		case observatory.PhaseReady:
			return maintained
		case observatory.PhaseActivating:
			best = activating
		case observatory.PhaseDeactivating:
			if best == idle {
				best = releasing
			}
		}
	}
	return best
}

func (o *operator) telescopeStatus(t *tree, telescope *observatory.Telescope, composed map[string]deviceStatus, guiders map[string]observatory.GuiderStatus) observatory.TelescopeStatus {
	name := telescope.Metadata.Name
	phase, holder := o.telescopePhase(t, name)
	next := observatory.TelescopeStatus{
		ObservedGeneration: telescope.Metadata.Generation,
		Phase:              phase,
		Server:             o.serverStatus(t, serverRef{observatory.TelescopeKind, name}),
		Devices:            refs(directDevices(t, observatory.TelescopeKind, name), composed),
	}
	message := "Not reserved"
	if holder != nil {
		next.Reservation = &observatory.ReservationRef{Name: holder.Metadata.Name, Holder: holder.Spec.Holder}
		message = fmt.Sprintf("%s for Reservation %s", firstNonEmpty(string(holder.Status.Phase), string(observatory.ReservationScheduled)), holder.Metadata.Name)
	}
	for _, tube := range sortedNames(t.tubes) {
		if t.tubes[tube].Spec.Telescope == name {
			next.Tubes = append(next.Tubes, tube)
		}
	}
	for _, train := range sortedNames(t.trains) {
		if object := t.trains[train]; object.Spec.Telescope == name {
			next.Trains = append(next.Trains, observatory.TrainRef{
				Name: train, OpticalTube: object.Spec.OpticalTube,
				Devices: refs(t.trainDevices(train), composed),
			})
		}
	}
	if guider := t.guiderOf(name); guider != nil {
		next.Guider = guiderRef(guider.Metadata.Name, guiders[guider.Metadata.Name])
		next.Display.Guider = guiderColumn(next.Guider)
	}
	conditions := []observatory.Condition{
		parentCondition(t.missingObservatory(telescope.Spec.Observatory)),
		readyCondition(phase, message),
		o.activity.condition(telescopeKey(name)),
	}
	if horizon := telescope.Spec.Horizon; horizon != "" {
		conditions = append(conditions, t.horizonFound(horizon))
	}
	next.Procedures = o.runs.list(recordKey(observatory.TelescopeKind, telescope.Metadata))
	conditions = withAttention(conditions, next.Procedures, telescope.Status.Conditions,
		o.activeSince(resource{kind: observatory.TelescopeKind, meta: telescope.Metadata}))
	next.Conditions = withGeneration(conditions, next.ObservedGeneration)
	return next
}

func (o *operator) observatoryStatus(t *tree, site *observatory.Observatory, composed map[string]deviceStatus) observatory.ObservatoryStatus {
	name := site.Metadata.Name
	ref := serverRef{observatory.ObservatoryKind, name}
	devices := t.devicesOn(ref)
	next := observatory.ObservatoryStatus{
		ObservedGeneration: site.Metadata.Generation,
		Server:             o.serverStatus(t, ref),
		Devices:            refs(devices, composed),
		Weather:            observatory.SafetyUnknown,
		Display: observatory.ObservatoryDisplay{
			Latitude:  latitude(site.Spec.Location.Latitude),
			Longitude: longitude(site.Spec.Location.Longitude),
		},
	}
	// The observatory is Deactivating while a reservation of one of its
	// telescopes deactivates and no telescope in it keeps it
	// (keepsObservatory). The same test decides the StopSite step
	// (siteStopFor in sitestop.go), so the observatory reads
	// Deactivating exactly when one of the deactivating reservations is
	// to disconnect its devices.
	var keeping bool
	var leaving []string
	for _, telescope := range sortedNames(t.telescopes) {
		if t.telescopes[telescope].Spec.Observatory == name {
			next.Telescopes = append(next.Telescopes, telescope)
			if holder, ok := o.claims.holderOf(telescope); ok {
				next.Reservations = append(next.Reservations, holder)
			}
			switch phase, _ := o.telescopePhase(t, telescope); {
			case phase == observatory.PhaseDeactivating:
				holder, _ := o.claims.holderOf(telescope)
				leaving = append(leaving, holder)
			case keepsObservatory(phase):
				keeping = true
			}
		}
	}
	next.Phase = observatory.PhaseIdle
	message := "Not reserved"
	if len(leaving) > 0 && !keeping {
		next.Phase, message = observatory.PhaseDeactivating, "Deactivating for "+reservationList(leaving)
	} else if len(next.Reservations) > 0 {
		next.Phase, message = observatory.PhaseReady, "Connected every device"
		for _, d := range devices {
			if phase := composed[d.key()].Phase; phase != observatory.DeviceConnected {
				next.Phase, message = observatory.PhaseActivating, fmt.Sprintf("Waiting for %s %s (%s)", d.kind.Name, d.name(), phase)
				break
			}
		}
	}
	next.Weather = worstWeather(devices, composed)
	active := o.activity.condition(observatoryKey(name))
	if active.Status == observatory.ConditionTrue && len(next.Reservations) > 0 {
		// The reservation that activated the observatory may have ended
		// while other reservations hold telescopes in it, so the message
		// lists the current holders. A changed message is not a
		// transition, so the condition keeps its transition time.
		active.Message = "Active for " + reservationList(next.Reservations)
	}
	conditions := []observatory.Condition{readyCondition(next.Phase, message), active}
	if locks := o.planLocks(t, site).condition; locks != nil {
		conditions = append(conditions, *locks)
	}
	next.Procedures = o.runs.list(recordKey(observatory.ObservatoryKind, site.Metadata))
	conditions = withAttention(conditions, next.Procedures, site.Status.Conditions,
		o.activeSince(resource{kind: observatory.ObservatoryKind, meta: site.Metadata}))
	next.Conditions = withGeneration(conditions, next.ObservedGeneration)
	return next
}

// reservationList formats reservation names as "Reservation east" or
// "Reservations east, west".
func reservationList(names []string) string {
	if len(names) == 1 {
		return "Reservation " + names[0]
	}
	return "Reservations " + strings.Join(names, ", ")
}

// worstWeather returns the worst Safety that a weather station reports
// in SAFETY_STATUS, or Unknown when none reports it.
func worstWeather(devices []*device, composed map[string]deviceStatus) observatory.Safety {
	rank := []observatory.Safety{observatory.SafetySafe, observatory.SafetyWarning, observatory.SafetyDanger}
	worst := -1
	for _, d := range devices {
		if s, ok := composed[d.key()].Standard.(*observatory.WeatherStationStandard); ok && s.Safety != nil {
			worst = max(worst, slices.Index(rank, typedSafety(*s.Safety)))
		}
	}
	if worst < 0 {
		return observatory.SafetyUnknown
	}
	return rank[worst]
}

func trainStatus(t *tree, train *observatory.OpticalTrain, composed map[string]deviceStatus) observatory.OpticalTrainStatus {
	missingParent := t.missingTelescope(train.Spec.Telescope)
	if _, ok := t.tubes[train.Spec.OpticalTube]; !ok && missingParent == "" {
		missingParent = "OpticalTube " + train.Spec.OpticalTube
	}
	return observatory.OpticalTrainStatus{
		ObservedGeneration: train.Metadata.Generation,
		Devices:            refs(t.trainDevices(train.Metadata.Name), composed),
		Conditions:         withGeneration([]observatory.Condition{parentCondition(missingParent)}, train.Metadata.Generation),
	}
}

func tubeStatus(t *tree, tube *observatory.OpticalTube) observatory.OpticalTubeStatus {
	next := observatory.OpticalTubeStatus{
		ObservedGeneration: tube.Metadata.Generation,
		Display: observatory.OpticalTubeDisplay{
			Aperture:    quantity(tube.Spec.Aperture, 1, "mm"),
			FocalLength: quantity(tube.Spec.FocalLength, 1, "mm"),
		},
	}
	for _, train := range sortedNames(t.trains) {
		if t.trains[train].Spec.OpticalTube == tube.Metadata.Name {
			next.Trains = append(next.Trains, train)
		}
	}
	next.Conditions = withGeneration([]observatory.Condition{parentCondition(t.missingTelescope(tube.Spec.Telescope))}, next.ObservedGeneration)
	return next
}

func sortedNames[T any](m map[string]*T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
