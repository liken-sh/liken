package main

// The API calls for Television. Each write is a server-side apply
// that states only the fields its writer owns. Four writers share one
// Television: the person who writes the spec; the Deployment, which
// writes the facts it derives from the CECBus, the Reachable and
// InCharge conditions, and, under a manager of its own, the session;
// the node workload that sends the bus's commands, which writes
// powerGeneration and the PowerApplied condition; and the node workload
// that speaks for the session's Display, which writes wokeAt and the
// WakeApplied condition under one manager, and standbyAt and the
// StandbyApplied condition under another. The node workload that sends
// the bus's commands writes powerRead under a manager of its own. The conditions are a map
// keyed by type, so each writer's apply leaves the other writers'
// conditions in place. Discovery
// creates its Television with a create, so it writes no spec field
// after that.

import (
	"context"
	"encoding/json"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// A Television is cluster-scoped, like a Receiver and a CECBus.
const televisionsPath = "/apis/" + equipmentAPIVersion + "/televisions"

func televisionPath(name string) string {
	return televisionsPath + "/" + name
}

// ListTelevisions reads every Television. A cluster without the
// Television definition answers the list with not found; that is a
// cluster with no Television, and the CECBus work of both workloads
// goes on. The empty list has no resourceVersion, so a caller starts no
// watch on a collection that does not exist.
func ListTelevisions(c *Client) (*TelevisionList, error) {
	list := &TelevisionList{}
	err := c.RequestJSON(http.MethodGet, televisionsPath, nil, list)
	if err == ErrNotFound {
		return &TelevisionList{}, nil
	}
	if err != nil {
		return nil, err
	}
	return list, nil
}

// readTelevisions answers every Television from the watch's store,
// and lists them from the API server while the store has nothing to
// give (objectcache.go).
func readTelevisions(c *Client, held *watchStore) (*TelevisionList, error) {
	if view := held.view(); view.ready() {
		items, err := currentList[Television](c, heldObjects{view: view, versions: c.versions.televisions}, televisionPath)
		return &TelevisionList{Items: items}, err
	}
	return ListTelevisions(c)
}

// watchTelevisions wakes a loop on every change to a Television, its
// status included, because each workload acts on status the other
// writes. restarted is called for each watch opened again after the
// first. A cluster without the Television definition has no
// Television, and the watch holds none until the definition arrives.
func watchTelevisions(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), held *watchStore) {
	watchCollection(ctx, client, televisionResource, "", apierrors.IsNotFound, wakeOnEvery(wake), func() { poke(wake) }, restarted, held)
}

// applyTelevision sends one apply body under one field manager. force
// settles a conflict in the manager's favour, because each field these
// bodies state has one writer.
func applyTelevision(c *Client, name, path, manager string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = written[Television](c.versions.televisions, name, func() (*Television, error) {
		answer := &Television{}
		return answer, c.requestJSON(http.MethodPatch, path+"?fieldManager="+manager+"&force=true", applyContentType, encoded, answer)
	})
	return err
}

// televisionApply is the identity every apply body carries.
func televisionApply(name string) (string, string, ObjectMeta) {
	return equipmentAPIVersion, "Television", ObjectMeta{Name: name}
}

// ApplyTelevisionDerived writes what the Deployment derives. A fact
// the apply leaves out is removed, because the Deployment owns it: a
// TV that stops answering loses its power instead of keeping a stale
// one.
func ApplyTelevisionDerived(c *Client, name string, derived televisionDerived) error {
	type status struct {
		CEC           *TelevisionCECStatus `json:"cec,omitempty"`
		Power         string               `json:"power,omitempty"`
		ActiveSource  string               `json:"activeSource,omitempty"`
		ActiveDisplay string               `json:"activeDisplay,omitempty"`
		Displays      []TelevisionDisplay  `json:"displays,omitempty"`
		Conditions    []Condition          `json:"conditions"`
	}
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     status     `json:"status"`
	}{Status: status{CEC: derived.cec, Power: derived.power, ActiveSource: derived.activeSource, ActiveDisplay: derived.activeDisplay, Displays: derived.displays, Conditions: []Condition{derived.reachable, derived.inCharge}}}
	body.APIVersion, body.Kind, body.Metadata = televisionApply(name)
	return applyTelevision(c, name, televisionPath(name)+"/status", fieldManager, body)
}

// ApplyTelevisionPower writes what the node workload applied: the
// generation whose spec.power it applied and the PowerApplied
// condition. It applies under the machine's field manager, and force
// moves the two fields to another machine's manager when another
// adapter sends the bus's commands. The body states the object's uid,
// so the API server refuses the write with a conflict when the
// Television was deleted and created again under the same name: the
// result belongs to the old object.
func ApplyTelevisionPower(c *Client, television *Television, machine string, generation int64, condition Condition) error {
	type status struct {
		PowerGeneration int64       `json:"powerGeneration"`
		Conditions      []Condition `json:"conditions"`
	}
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     status     `json:"status"`
	}{Status: status{PowerGeneration: generation, Conditions: []Condition{condition}}}
	body.APIVersion, body.Kind, body.Metadata = televisionApply(television.Metadata.Name)
	body.Metadata.UID = television.Metadata.UID
	return applyTelevision(c, television.Metadata.Name, televisionPath(television.Metadata.Name)+"/status", cecFieldManager(machine), body)
}

// cecWakeFieldManager is the field manager of the node workload on one
// machine for the wake. It is not the machine's power manager, because
// an apply removes each field its manager owns and does not state, and
// the power and the wake are written apart, often by two machines.
func cecWakeFieldManager(machine string) string {
	return "equipment-operator-wake-" + machine
}

// ApplyTelevisionWake writes what the node workload's wake did: the
// status.session.wokeAt it ran and the WakeApplied condition. The body
// states the object's uid for the same reason ApplyTelevisionPower
// does.
func ApplyTelevisionWake(c *Client, television *Television, machine, wokeAt string, condition Condition) error {
	type status struct {
		WokeAt     string      `json:"wokeAt"`
		Conditions []Condition `json:"conditions"`
	}
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     status     `json:"status"`
	}{Status: status{WokeAt: wokeAt, Conditions: []Condition{condition}}}
	body.APIVersion, body.Kind, body.Metadata = televisionApply(television.Metadata.Name)
	body.Metadata.UID = television.Metadata.UID
	return applyTelevision(c, television.Metadata.Name, televisionPath(television.Metadata.Name)+"/status", cecWakeFieldManager(machine), body)
}

// cecStandbyFieldManager is the field manager of the node workload on
// one machine for the standby. It is not the wake's manager, because
// the wake and the standby are written apart, and an apply removes
// each field its manager owns and does not state.
func cecStandbyFieldManager(machine string) string {
	return "equipment-operator-standby-" + machine
}

// ApplyTelevisionStandby writes what the node workload's standby did:
// the status.session.standbyAt it ran and the StandbyApplied condition.
// The body states the object's uid for the same reason
// ApplyTelevisionPower does.
func ApplyTelevisionStandby(c *Client, television *Television, machine, standbyAt string, condition Condition) error {
	type status struct {
		StandbyAt  string      `json:"standbyAt"`
		Conditions []Condition `json:"conditions"`
	}
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     status     `json:"status"`
	}{Status: status{StandbyAt: standbyAt, Conditions: []Condition{condition}}}
	body.APIVersion, body.Kind, body.Metadata = televisionApply(television.Metadata.Name)
	body.Metadata.UID = television.Metadata.UID
	return applyTelevision(c, television.Metadata.Name, televisionPath(television.Metadata.Name)+"/status", cecStandbyFieldManager(machine), body)
}

// cecPowerReadFieldManager is the field manager of the node workload on
// one machine for the answer to a power press's read. It is not the
// machine's power manager, because an apply removes each field its
// manager owns and does not state, and the answer and the applied
// generation are written apart.
func cecPowerReadFieldManager(machine string) string {
	return "equipment-operator-powerread-" + machine
}

// ApplyTelevisionPowerRead writes the node workload's answer to one
// status.session.powerReadAt: the request, and the power the TV
// reported, empty when it did not answer. The body states the object's
// uid for the same reason ApplyTelevisionPower does.
func ApplyTelevisionPowerRead(c *Client, television *Television, machine, at, power string) error {
	type status struct {
		PowerRead TelevisionPowerRead `json:"powerRead"`
	}
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     status     `json:"status"`
	}{Status: status{PowerRead: TelevisionPowerRead{At: at, Power: power}}}
	body.APIVersion, body.Kind, body.Metadata = televisionApply(television.Metadata.Name)
	body.Metadata.UID = television.Metadata.UID
	return applyTelevision(c, television.Metadata.Name, televisionPath(television.Metadata.Name)+"/status", cecPowerReadFieldManager(machine), body)
}

// sessionFieldManager is the Deployment's field manager for
// status.session. The Deployment's derived status is written under
// fieldManager, and an apply removes each field its manager owns and
// does not state, so the session has a manager of its own: a write of
// the derived status never removes the session, and a write of the
// session never removes the derived status. The node workloads' fields
// have managers of their own too. A person's apply of the spec reaches
// no status field, because the status subresource splits the two.
const sessionFieldManager = "equipment-operator-session"

// ApplyTelevisionSession writes the Deployment's status.session. A nil
// session removes the field, because the apply then states no field
// its manager owns. A status write changes no generation, so the node
// workload's spec.power key does not move.
func ApplyTelevisionSession(c *Client, name string, session *TelevisionSession) error {
	type status struct {
		Session *TelevisionSession `json:"session,omitempty"`
	}
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     status     `json:"status"`
	}{Status: status{Session: session}}
	body.APIVersion, body.Kind, body.Metadata = televisionApply(name)
	return applyTelevision(c, name, televisionPath(name)+"/status", sessionFieldManager, body)
}

// CreateDiscoveredTelevision creates the Television the Deployment
// makes for a bus's TV, under the bus's own name. It states the bus
// and nothing else, so it asks no power of the TV, and it carries the
// discovered label. It is a create and not an apply: an object that
// already has the name is left as it is, and the API server's conflict
// for it is no error. It answers whether this call is the one that
// created the Television, so a caller that logs a creation logs one
// only when it happened.
func CreateDiscoveredTelevision(c *Client, bus string) (bool, error) {
	name := discoveredTelevisionName(bus)
	object := Television{Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: bus}}}
	object.APIVersion, object.Kind, object.Metadata = televisionApply(name)
	object.Metadata.Labels = map[string]string{discoveredLabel: discoveredCECLabelValue}
	encoded, err := json.Marshal(object)
	if err != nil {
		return false, err
	}
	_, err = written[Television](c.versions.televisions, name, func() (*Television, error) {
		answer := &Television{}
		return answer, c.RequestJSON(http.MethodPost, televisionsPath, encoded, answer)
	})
	if err == ErrConflict {
		return false, nil
	}
	return err == nil, err
}

// DeleteTelevision removes one Television. A name that is already
// gone is not an error.
func DeleteTelevision(c *Client, name string) error {
	return c.versions.televisions.send(name, func() (string, error) {
		return "", deleteObject(c, televisionPath(name), "Television "+name)
	})
}
