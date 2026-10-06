package main

// The status of the resources above the devices: what the operator
// builds of the tree by looking down from each resource, and the phase
// of the reservation that holds each telescope.

import (
	"fmt"
	"slices"

	"github.com/liken-sh/liken/observatory-operator/observatory"
)

// The conditions of a resource with no parent of its own to look for.
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

// serverStatus answers a server's endpoint and pod, while its pod
// exists.
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

// refs lists devices with their phases.
func refs(devices []*device, composed map[string]deviceStatus) []observatory.DeviceRef {
	var out []observatory.DeviceRef
	for _, d := range devices {
		out = append(out, observatory.DeviceRef{Kind: d.kind.Name, Name: d.name(), Phase: composed[d.key()].Phase})
	}
	return out
}

// directDevices answers the devices whose parent field names a
// resource of one kind and name, as opposed to through a train.
func directDevices(t *tree, kind observatory.Kind, name string) []*device {
	var out []*device
	for _, d := range t.devices {
		if p := d.parent(); p.Kind == kind && p.Name == name {
			out = append(out, d)
		}
	}
	return out
}

// telescopePhase answers a telescope's phase from the reservation that
// holds it.
func (o *operator) telescopePhase(t *tree, telescope string) (observatory.Phase, *observatory.Reservation) {
	holder, ok := o.claims.holderOf(telescope)
	if !ok {
		return observatory.PhaseInventory, nil
	}
	r, ok := t.reservations[holder]
	if !ok {
		return observatory.PhaseInventory, nil
	}
	switch r.Status.Phase {
	case observatory.ReservationReady:
		return observatory.PhaseReady, r
	case observatory.ReservationDeactivating:
		return observatory.PhaseDeactivating, r
	case observatory.ReservationFailed:
		return observatory.PhaseError, r
	}
	return observatory.PhaseActivating, r
}

func (o *operator) telescopeStatus(t *tree, telescope *observatory.Telescope, composed map[string]deviceStatus) observatory.TelescopeStatus {
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
	for _, guider := range sortedNames(t.guiders) {
		if t.guiders[guider].Spec.Telescope == name {
			next.Guider = &observatory.GuiderRef{Name: guider, Ready: false, Reason: observatory.ReasonNotImplemented}
			next.Display.Guider = next.Guider.Reason
			break
		}
	}
	next.Conditions = withGeneration([]observatory.Condition{
		parentCondition(t.missingObservatory(telescope.Spec.Observatory)),
		readyCondition(phase, message),
	}, next.ObservedGeneration)
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
	for _, telescope := range sortedNames(t.telescopes) {
		if t.telescopes[telescope].Spec.Observatory == name {
			next.Telescopes = append(next.Telescopes, telescope)
			if holder, ok := o.claims.holderOf(telescope); ok {
				next.Reservations = append(next.Reservations, holder)
			}
		}
	}
	next.Phase = observatory.PhaseInventory
	message := "Not reserved"
	if len(next.Reservations) > 0 {
		next.Phase, message = observatory.PhaseReady, "Connected every device"
		for _, d := range devices {
			if phase := composed[d.key()].Phase; phase != observatory.DeviceConnected {
				next.Phase, message = observatory.PhaseActivating, fmt.Sprintf("Waiting for %s %s (%s)", d.kind.Name, d.name(), phase)
				break
			}
		}
	}
	next.Weather = worstWeather(devices, composed)
	next.Conditions = withGeneration([]observatory.Condition{readyCondition(next.Phase, message)}, next.ObservedGeneration)
	return next
}

// worstWeather answers the worst verdict of the weather stations that
// report one, and Unknown when none does.
func worstWeather(devices []*device, composed map[string]deviceStatus) observatory.Safety {
	rank := []observatory.Safety{observatory.SafetySafe, observatory.SafetyWarning, observatory.SafetyDanger}
	worst := -1
	for _, d := range devices {
		if readings, ok := composed[d.key()].Readings.(observatory.WeatherStationReadings); ok {
			worst = max(worst, slices.Index(rank, readings.Safety))
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

// guiderStatus says that the guider's pod is not built. Plan 09 builds
// it.
func guiderStatus(t *tree, guider *observatory.Guider) observatory.GuiderStatus {
	missingParent := t.missingTelescope(guider.Spec.Telescope)
	if _, ok := t.trains[guider.Spec.OpticalTrain]; !ok && missingParent == "" {
		missingParent = "OpticalTrain " + guider.Spec.OpticalTrain
	}
	return observatory.GuiderStatus{
		ObservedGeneration: guider.Metadata.Generation,
		Phase:              observatory.PhaseInventory,
		Conditions: withGeneration([]observatory.Condition{
			parentCondition(missingParent),
			condition(observatory.ConditionReady, observatory.ConditionFalse, observatory.ReasonNotImplemented,
				"Not built yet: plan 09 of observatory-operator builds the PHD2 pod"),
		}, guider.Metadata.Generation),
	}
}

func sortedNames[T any](m map[string]*T) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
