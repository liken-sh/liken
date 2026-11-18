package main

// The runner reads each Target from the watch's store and writes its
// status with the resourceVersion it read. When that copy is stale,
// the API server returns 409, and informer.SettleStatus reads the
// Target again and applies the change to the new copy. So a status
// write never overwrites another runner's claim of the Target, or a
// promotion report written after the read.

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/liken-sh/liken/astrophotography-operator/astrophotography"
	"github.com/liken-sh/liken/kubernetes/conditions"
	"github.com/liken-sh/liken/kubernetes/events"
	"github.com/liken-sh/liken/kubernetes/informer"
)

func targetRef(t *astrophotography.Target) events.ObjectReference {
	return events.ObjectReference{
		APIVersion: astrophotography.APIVersion, Kind: astrophotography.TargetKind.Name,
		Namespace: t.Metadata.Namespace, Name: t.Metadata.Name, UID: t.Metadata.UID,
	}
}

// targets returns the store of Targets and the resourceVersions of the
// operator's own writes. The watch covers every Target, so the store
// is complete and a list reads from it.
func (o *operator) targets() informer.Held {
	view := o.stores.collections[astrophotography.TargetKind.Name].View()
	view.Whole = true
	return informer.Held{View: view, Versions: o.targetVersions}
}

// listTargets returns every target in a namespace, or in all
// namespaces when namespace is "", in the order of their keys.
func (o *operator) listTargets(namespace string) ([]astrophotography.Target, error) {
	all, err := informer.List[astrophotography.Target](o.client, o.targets(),
		"/apis/"+astrophotography.APIVersion+"/"+astrophotography.TargetKind.Plural,
		func(key string) string {
			namespace, name, _ := cutKey(key)
			return targetPath(namespace, name)
		})
	if err != nil {
		return nil, err
	}
	var out []astrophotography.Target
	for _, t := range all {
		if namespace == "" || t.Metadata.Namespace == namespace {
			out = append(out, t)
		}
	}
	return out, nil
}

// cutKey splits a store key into namespace and name.
func cutKey(key string) (namespace, name string, namespaced bool) {
	return strings.Cut(key, "/")
}

// readTarget returns one target, or apiclient.ErrNotFound when it does
// not exist.
func (o *operator) readTarget(namespace, name string) (*astrophotography.Target, error) {
	return informer.ReadOne[astrophotography.Target](o.client, o.targets(), namespace+"/"+name, targetPath(namespace, name))
}

// statusChange collects the condition transitions of one status
// update, so the Events can be posted after the write succeeds.
type statusChange struct {
	transitions []transition
}

type transition struct {
	condition conditions.Condition
	// bad is the status for which the Event is a Warning instead of
	// Normal. When it is empty, the Event is always Normal.
	bad conditions.Status
}

// set sets a condition on the target and records the transition when
// the condition changed.
func (s *statusChange) set(t *astrophotography.Target, next conditions.Condition, bad conditions.Status) {
	next.ObservedGeneration = t.Metadata.Generation
	if conditions.Set(&t.Status.Conditions, next) {
		s.transitions = append(s.transitions, transition{next, bad})
	}
}

// writeTarget calls apply on the Target and writes the status when
// apply changed it. apply returns false to leave the Target unchanged,
// for example when a fresh copy shows that another runner claimed it.
// writeTarget returns whether it wrote. Only after a write succeeds
// does it post one Event for each condition transition, so a failed
// write posts no Event.
func (o *operator) writeTarget(stored *astrophotography.Target, apply func(*astrophotography.Target, *statusChange) bool) (bool, error) {
	stored.APIVersion, stored.Kind = astrophotography.APIVersion, astrophotography.TargetKind.Name
	var change statusChange
	wrote, err := informer.SettleStatus(o.client, o.targetVersions, targetPath(stored.Metadata.Namespace, stored.Metadata.Name), stored,
		func(t *astrophotography.Target) bool {
			change = statusChange{}
			before, _ := json.Marshal(t.Status)
			if !apply(t, &change) {
				return false
			}
			after, _ := json.Marshal(t.Status)
			return !bytes.Equal(before, after)
		})
	if wrote {
		for _, tr := range change.transitions {
			o.recorder.Transition(targetRef(stored), tr.condition, tr.bad)
		}
	}
	return wrote, err
}

// failures are the reasons for an Imaging condition that is False
// because a person may need to act. Their Events are Warnings.
var failures = map[string]bool{
	astrophotography.ReasonNoTrain:         true,
	astrophotography.ReasonNoFilter:        true,
	astrophotography.ReasonSlewFailed:      true,
	astrophotography.ReasonCenteringFailed: true,
	astrophotography.ReasonGuidingFailed:   true,
	astrophotography.ReasonOutboxFull:      true,
	astrophotography.ReasonExposureFailed:  true,
}

// setImaging sets the Imaging condition: True while a reservation
// images the target, with the current step as the reason, and False
// otherwise.
func (s *statusChange) setImaging(t *astrophotography.Target, imaging bool, reason, message string) {
	status, bad := conditions.False, conditions.Status("")
	if imaging {
		status = conditions.True
	}
	if failures[reason] {
		bad = conditions.False
	}
	s.set(t, conditions.Condition{Type: astrophotography.ConditionImaging, Status: status, Reason: reason, Message: message}, bad)
}
