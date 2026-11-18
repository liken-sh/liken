package main

// The API calls for Television. Several writers share one Television,
// and each applies only the fields it owns:
//
//   - A person writes the spec.
//   - The Deployment writes the status it derives from the CECBus,
//     with the Reachable and InCharge conditions, under fieldManager,
//     and status.session under sessionFieldManager.
//   - The node workload on each machine writes the results of the
//     commands it sends: powerGeneration, wokeAt, standbyAt, powerRead,
//     screenAsk, and their conditions. Each kind of result has its own
//     field manager with the machine name in it. Each of these applies
//     sets metadata.uid, so the API server refuses it with a 409 when
//     the Television was deleted and created again under the same name
//     and the result belongs to the old object.
//
// An apply removes the fields that its manager set before and the body
// leaves out, so fields that are written at different times need
// separate managers. status.conditions is a list keyed by type, so each
// apply leaves the other managers' conditions in place.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

const televisionsPath = "/apis/" + equipmentAPIVersion + "/televisions"

func televisionPath(name string) string {
	return televisionsPath + "/" + name
}

// ListTelevisions lists every Television from the API server. When the
// Television CRD is not installed, the list returns 404, and
// ListTelevisions returns an empty list so that the caller's CECBus work
// continues.
func ListTelevisions(c *Client) (*TelevisionList, error) {
	list := &TelevisionList{}
	err := c.RequestJSON(http.MethodGet, televisionsPath, nil, list)
	if err == apiclient.ErrNotFound {
		return &TelevisionList{}, nil
	}
	if err != nil {
		return nil, err
	}
	return list, nil
}

// readTelevisions returns every Television from the watch's store, or
// from a list on the API server until the store is ready
// (objectcache.go). A 404 from that list returns an empty list, as in
// ListTelevisions.
func readTelevisions(c *Client, store *watchStore) (*TelevisionList, error) {
	items, err := informer.List[Television](c.Client, informer.Held{View: store.view(), Versions: c.versions.televisions}, televisionsPath, televisionPath)
	if errors.Is(err, apiclient.ErrNotFound) {
		return &TelevisionList{}, nil
	}
	return &TelevisionList{Items: items}, err
}

// watchTelevisions signals wake on every change to a Television,
// status included, because the Deployment and the node workload each act
// on status that the other writes. restarted is called each time the
// watch opens again after the first time. While the Television CRD is
// not installed, the store stays empty.
func watchTelevisions(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), store *watchStore) {
	watchCollection(ctx, client, informer.Source{Resource: televisionResource}, apierrors.IsNotFound, wakeOnEvery(wake), func() { sendWake(wake) }, restarted, store)
}

// applyTelevision sends one apply under manager. force=true is safe
// because each field that these bodies set has one writer.
func applyTelevision(c *Client, name, path, manager string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = memo.Written[Television](c.versions.televisions, name, func() (*Television, error) {
		answer := &Television{}
		return answer, c.Request(http.MethodPatch, path+"?fieldManager="+manager+"&force=true", applyContentType, encoded, answer)
	})
	return err
}

// televisionApply returns the apiVersion, kind, and metadata that every
// Television apply body starts with.
func televisionApply(name string) (string, string, ObjectMeta) {
	return equipmentAPIVersion, "Television", ObjectMeta{Name: name}
}

// ApplyTelevisionDerived writes the status that the Deployment derives
// from the CECBus. A field that derived leaves empty is removed, so a TV
// that stops answering does not keep a stale power.
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

// ApplyTelevisionPower records the generation of spec.power that the
// node workload applied, and the PowerApplied condition, under
// cecFieldManager. force=true moves these fields from another machine's
// manager when that machine's adapter no longer sends the bus's
// commands.
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

func cecWakeFieldManager(machine string) string {
	return "equipment-operator-wake-" + machine
}

// ApplyTelevisionWake records the wake that the node workload ran: the
// status.session.wokeAt it acted on, the WakeApplied condition, and the
// SystemAudioApplied condition when the wake sent a System Audio Mode
// Request. A wake that sends no request leaves SystemAudioApplied out of
// conditions, so the apply removes the condition of an earlier wake.
func ApplyTelevisionWake(c *Client, television *Television, machine, wokeAt string, conditions ...Condition) error {
	type status struct {
		WokeAt     string      `json:"wokeAt"`
		Conditions []Condition `json:"conditions"`
	}
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     status     `json:"status"`
	}{Status: status{WokeAt: wokeAt, Conditions: conditions}}
	body.APIVersion, body.Kind, body.Metadata = televisionApply(television.Metadata.Name)
	body.Metadata.UID = television.Metadata.UID
	return applyTelevision(c, television.Metadata.Name, televisionPath(television.Metadata.Name)+"/status", cecWakeFieldManager(machine), body)
}

func cecStandbyFieldManager(machine string) string {
	return "equipment-operator-standby-" + machine
}

// ApplyTelevisionStandby records the standby that the node workload
// ran: the status.session.standbyAt it acted on and the StandbyApplied
// condition.
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

func cecPowerReadFieldManager(machine string) string {
	return "equipment-operator-powerread-" + machine
}

// ApplyTelevisionPowerRead records the result of the power read that
// status.session.powerReadAt requested. at is that request's time, and
// power is the state the TV reported, or empty when it did not answer.
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

func cecScreenFieldManager(machine string) string {
	return "equipment-operator-screen-" + machine
}

// ApplyTelevisionScreenAsk records a request, heard on the bus, to wake
// or sleep the screen of the session's Player.
func ApplyTelevisionScreenAsk(c *Client, television *Television, machine string, ask TelevisionScreenAsk) error {
	type status struct {
		ScreenAsk TelevisionScreenAsk `json:"screenAsk"`
	}
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     status     `json:"status"`
	}{Status: status{ScreenAsk: ask}}
	body.APIVersion, body.Kind, body.Metadata = televisionApply(television.Metadata.Name)
	body.Metadata.UID = television.Metadata.UID
	return applyTelevision(c, television.Metadata.Name, televisionPath(television.Metadata.Name)+"/status", cecScreenFieldManager(machine), body)
}

// sessionFieldManager is the Deployment's field manager for
// status.session. It is separate from fieldManager, so an apply of the
// derived status does not remove the session, and an apply of the
// session does not remove the derived status.
const sessionFieldManager = "equipment-operator-session"

// ApplyTelevisionSession writes the Deployment's status.session, and a
// nil session removes it. A status write does not change
// metadata.generation, so the node workload does not read it as a new
// spec.power.
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

// CreateDiscoveredTelevision creates the Television for a CECBus's TV,
// named after the bus, with discoveredLabel and a spec that sets only
// the bus. It is a create and not an apply, so an existing Television of
// that name is left unchanged, and the 409 is not an error. It returns
// true only when this call created the Television.
func CreateDiscoveredTelevision(c *Client, bus string) (bool, error) {
	name := discoveredTelevisionName(bus)
	object := Television{Spec: TelevisionSpec{CEC: &TelevisionCEC{Bus: bus}}}
	object.APIVersion, object.Kind, object.Metadata = televisionApply(name)
	object.Metadata.Labels = map[string]string{discoveredLabel: discoveredCECLabelValue}
	encoded, err := json.Marshal(object)
	if err != nil {
		return false, err
	}
	_, err = memo.Written[Television](c.versions.televisions, name, func() (*Television, error) {
		answer := &Television{}
		return answer, c.RequestJSON(http.MethodPost, televisionsPath, encoded, answer)
	})
	if err == apiclient.ErrConflict {
		return false, nil
	}
	return err == nil, err
}

func DeleteTelevision(c *Client, name string) error {
	return c.versions.televisions.Send(name, func() (string, error) {
		return "", deleteObject(c, televisionPath(name), "Television "+name)
	})
}
