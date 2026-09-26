package main

// The API calls for Television. Each status write is a server-side
// apply that states only the fields its writer owns. Three writers
// share one Television: the person who writes the spec, the
// Deployment, which writes the facts it derives from the CECBus and
// the Reachable and InCharge conditions, and the node workload that
// sends the bus's commands, which writes powerGeneration and the
// PowerApplied condition. The conditions are a map keyed by type, so
// each writer's apply leaves the other writer's conditions in place.
// Discovery creates its Television with a create, so it writes no spec
// field after that.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// watchTelevisions wakes a loop on every change to a Television.
// restarted is called for each watch opened again after the first.
func watchTelevisions(ctx context.Context, client *Client, resourceVersion string, wake chan<- struct{}, restarted func()) {
	watchCollection(ctx, client, televisionsPath, resourceVersion, wake, restarted, func() (string, error) {
		list, err := ListTelevisions(client)
		if err != nil {
			return "", err
		}
		return list.Metadata.ResourceVersion, nil
	})
}

// applyTelevision sends one apply body under one field manager. force
// settles a conflict in the manager's favour, because each field these
// bodies state has one writer.
func applyTelevision(c *Client, path, manager string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	written := &Television{}
	return c.requestJSON(http.MethodPatch, path+"?fieldManager="+manager+"&force=true", applyContentType, encoded, written)
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
		CEC        *TelevisionCECStatus `json:"cec,omitempty"`
		Power      string               `json:"power,omitempty"`
		Displays   []TelevisionDisplay  `json:"displays,omitempty"`
		Conditions []Condition          `json:"conditions"`
	}
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     status     `json:"status"`
	}{Status: status{CEC: derived.cec, Power: derived.power, Displays: derived.displays, Conditions: []Condition{derived.reachable, derived.inCharge}}}
	body.APIVersion, body.Kind, body.Metadata = televisionApply(name)
	return applyTelevision(c, televisionPath(name)+"/status", fieldManager, body)
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
	return applyTelevision(c, televisionPath(television.Metadata.Name)+"/status", cecFieldManager(machine), body)
}

// CreateDiscoveredTelevision creates the Television the Deployment
// makes for a bus's TV, under the bus's own name. It states the bus
// and nothing else, so it asks no power of the TV, and it carries the
// discovered label. It is a create and not an apply: an object that
// already has the name is left as it is, and the API server's conflict
// for it is no error.
func CreateDiscoveredTelevision(c *Client, bus string) error {
	name := discoveredTelevisionName(bus)
	object := Television{Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: bus}}}
	object.APIVersion, object.Kind, object.Metadata = televisionApply(name)
	object.Metadata.Labels = map[string]string{discoveredLabel: discoveredCECLabelValue}
	encoded, err := json.Marshal(object)
	if err != nil {
		return err
	}
	err = c.RequestJSON(http.MethodPost, televisionsPath, encoded, &Television{})
	if err == ErrConflict {
		return nil
	}
	return err
}

// DeleteTelevision removes one Television. A name that is already
// gone is not an error.
func DeleteTelevision(c *Client, name string) error {
	resp, err := c.send(context.Background(), http.MethodDelete, televisionPath(name), "", nil)
	if err != nil {
		return err
	}
	defer drain(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message := responseText(resp.Body)
		return fmt.Errorf("deleting Television %s: %s: %s", name, resp.Status, message)
	}
	return nil
}
