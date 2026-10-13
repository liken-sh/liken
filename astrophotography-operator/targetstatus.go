package main

// The reads and the status writes of a Target. The runner reads each
// target from the watch's store, and writes its status with the
// resourceVersion it read, so a write from a copy that another writer
// changed gets a 409. informer.SettleStatus then reads the target again
// and composes the change once more from the fresh copy, so a claim
// that another runner took first, or a promotion report that landed
// between the read and the write, is never overwritten.
//
// Each condition transition posts one Event on the target, after the
// write lands, so a refused write posts nothing (root plan 78).

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

// targets is the store of the Targets and the memo of the operator's
// writes to them. The watch takes every target, so the store is whole.
func (o *operator) targets() informer.Held {
	view := o.stores.collections[astrophotography.TargetKind.Name].View()
	view.Whole = true
	return informer.Held{View: view, Versions: o.targetVersions}
}

// listTargets answers every target in a namespace, or in every
// namespace for "", in the order of their keys.
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

// cutKey splits a store's key into its namespace and its name.
func cutKey(key string) (namespace, name string, namespaced bool) {
	return strings.Cut(key, "/")
}

// readTarget answers one target, and apiclient.ErrNotFound when it is
// gone.
func (o *operator) readTarget(namespace, name string) (*astrophotography.Target, error) {
	return informer.ReadOne[astrophotography.Target](o.client, o.targets(), namespace+"/"+name, targetPath(namespace, name))
}

// statusChange collects the condition transitions of one composed
// status, to post once the write lands.
type statusChange struct {
	transitions []transition
}

type transition struct {
	condition conditions.Condition
	bad       conditions.Status
}

// set puts a condition in the target's list, and records the
// transition when there is one.
func (s *statusChange) set(t *astrophotography.Target, next conditions.Condition, bad conditions.Status) {
	next.ObservedGeneration = t.Metadata.Generation
	if conditions.Set(&t.Status.Conditions, next) {
		s.transitions = append(s.transitions, transition{next, bad})
	}
}

// writeTarget composes a target's status with apply on the copy held,
// and writes it when it changed. apply answers false to leave the
// target alone, such as when a fresh copy shows that another runner
// claimed it. writeTarget answers whether a write landed, and posts the
// Event of each transition of the status that landed.
func (o *operator) writeTarget(held *astrophotography.Target, apply func(*astrophotography.Target, *statusChange) bool) (bool, error) {
	held.APIVersion, held.Kind = astrophotography.APIVersion, astrophotography.TargetKind.Name
	var change statusChange
	wrote, err := informer.SettleStatus(o.client, o.targetVersions, targetPath(held.Metadata.Namespace, held.Metadata.Name), held,
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
			o.recorder.Transition(targetRef(held), tr.condition, tr.bad)
		}
	}
	return wrote, err
}

// failures are the reasons of an Imaging that is False because a person
// may need to act. Their transitions are Warnings.
var failures = map[string]bool{
	astrophotography.ReasonNoTrain:         true,
	astrophotography.ReasonNoFilter:        true,
	astrophotography.ReasonSlewFailed:      true,
	astrophotography.ReasonCenteringFailed: true,
	astrophotography.ReasonGuidingFailed:   true,
	astrophotography.ReasonOutboxFull:      true,
	astrophotography.ReasonExposureFailed:  true,
}

// setImaging sets the Imaging condition: True with the step as its
// reason while a reservation images the target, and False with the
// reason it does not.
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
