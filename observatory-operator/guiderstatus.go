package main

// The status of a Guider: its pod, and what PHD2 reports on its event
// server. The status writer composes it with the others, at most once
// a statusWindow, so PHD2's guide steps, about one a second, cost at
// most one write a second.

import (
	"fmt"
	"time"

	"github.com/liken-sh/liken/observatory-operator/observatory"
	"github.com/liken-sh/liken/observatory-operator/phd2"
)

// guiderKey is a guider's key among the operator's faults.
func guiderKey(name string) string { return observatory.GuiderKind.Name + "/" + name }

// guiderFault records the last failure of the steady runner's work on a
// guider, or clears it.
func (o *operator) guiderFault(name string, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err == nil {
		delete(o.faults, guiderKey(name))
		return
	}
	o.faults[guiderKey(name)] = err.Error()
}

func (o *operator) guiderStatus(t *tree, guider *observatory.Guider) observatory.GuiderStatus {
	missingParent := t.missingTelescope(guider.Spec.Telescope)
	if _, ok := t.trains[guider.Spec.OpticalTrain]; !ok && missingParent == "" {
		missingParent = "OpticalTrain " + guider.Spec.OpticalTrain
	}
	next := observatory.GuiderStatus{ObservedGeneration: guider.Metadata.Generation}
	name := guiderName(guider)
	p, hasPod := t.pods[name]
	var s phd2.State
	if hasPod {
		endpoint := guiderEndpoint(o.namespace, guider.Metadata.Name)
		next.Endpoint, next.Pod, next.Node = &endpoint, name, p.Spec.NodeName
		if conn, ok := o.guiderConns.get(name); ok {
			s = conn.client.State()
		}
	}
	guiderReadings(&next, s)
	o.mu.Lock()
	fault := o.faults[guiderKey(guider.Metadata.Name)]
	o.mu.Unlock()
	phase, holder := o.telescopePhase(t, guider.Spec.Telescope)
	message := ""
	switch {
	case phase == observatory.PhaseInventory || holder == nil:
		phase, message = observatory.PhaseInventory, "Not reserved"
	case fault != "":
		phase, message = observatory.PhaseError, "Failed: "+fault
	case hasPod && p.ready() && s.Open && equipment(s):
		phase, message = observatory.PhaseReady, "PHD2 is connected to its camera and mount"
	case phase == observatory.PhaseError:
		message = "Reservation " + holder.Metadata.Name + " failed"
	default:
		message = guiderWaiting(name, p, hasPod, s, phase == observatory.PhaseReady)
		if phase == observatory.PhaseReady {
			phase = observatory.PhaseActivating
		}
	}
	next.Phase = phase
	next.Conditions = withGeneration([]observatory.Condition{
		parentCondition(missingParent),
		readyCondition(phase, message),
	}, next.ObservedGeneration)
	return next
}

// guiderWaiting says what a guider that is not ready waits for. kept
// is true while the reservation is Ready, when its runner, not
// StartGuider, creates a pod that is gone.
func guiderWaiting(name string, p *pod, hasPod bool, s phd2.State, kept bool) string {
	switch {
	case !hasPod && kept:
		return "Creating pod " + name
	case !hasPod:
		return "Waiting for StartGuider to create pod " + name
	case !p.ready():
		return fmt.Sprintf("Waiting for pod %s (%s)", name, firstNonEmpty(p.Status.Phase, "Pending"))
	case !s.Open:
		return "Waiting for PHD2's event server on " + name
	}
	return "Waiting for PHD2 to connect its camera and mount"
}

// guiderReadings copies what PHD2 reports into a Guider's status. PHD2
// measures distances in pixels, and the status gives them in
// arc-seconds, so the RMS needs the pixel scale.
func guiderReadings(next *observatory.GuiderStatus, s phd2.State) {
	if !s.Open {
		return
	}
	next.State = observatory.GuiderState(s.AppState)
	next.Calibrated = s.Calibrated
	next.PixelScale = s.PixelScale
	if s.PixelScale != nil && s.RMS.Steps > 0 {
		scale := *s.PixelScale
		next.RMS = &observatory.GuiderRMS{
			RA: s.RMS.RA * scale, Dec: s.RMS.Dec * scale, Total: s.RMS.Total * scale,
			Steps: int32(s.RMS.Steps), Since: s.RMS.Since.Truncate(time.Second),
		}
		next.Display.RMS = quantity(next.RMS.Total, 2, "arcsec")
	}
	if s.Step != nil {
		next.Star = &observatory.GuideStar{SNR: s.Step.SNR, HFD: s.Step.HFD}
		at := s.Step.Time.Truncate(time.Second)
		next.LastStepTime = &at
	}
	if s.Alert != nil {
		next.Alert = &observatory.GuiderAlert{Message: s.Alert.Message, Type: s.Alert.Type, Time: s.Alert.Time.Truncate(time.Second)}
	}
}

// guiderRef answers the Guider in its telescope's status.
func guiderRef(name string, s observatory.GuiderStatus) *observatory.GuiderRef {
	ready := conditionOf(s.Conditions, observatory.ConditionReady)
	return &observatory.GuiderRef{
		Name: name, Ready: ready.Status == observatory.ConditionTrue, Reason: ready.Reason,
		Phase: s.Phase, State: s.State,
	}
}

// guiderColumn answers the Telescope's Guider column: the phase, and
// PHD2's state while the operator reads it.
func guiderColumn(ref *observatory.GuiderRef) string {
	if ref.State == "" {
		return string(ref.Phase)
	}
	return string(ref.Phase) + ", " + string(ref.State)
}
