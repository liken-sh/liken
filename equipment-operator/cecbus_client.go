package main

// The API calls for CECBus, and the one read of a Display. Each write
// is a server-side apply that states only the fields its writer owns.
// Three kinds of writer share one CECBus: the person who writes the
// spec, the node workload on each adapter's machine, which writes its
// own entry under status.adapters, and the Deployment, which writes
// status.devices and the conditions. Each one applies under its own
// field manager, so no writer removes a field another owns.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// A CECBus is cluster-scoped, like a Receiver.
const cecBusesPath = "/apis/" + equipmentAPIVersion + "/cecbuses"

func cecBusPath(name string) string {
	return cecBusesPath + "/" + name
}

// cecFieldManager is the field manager of the node workload on one
// machine. The machine is in the name, because two node workloads each
// own one entry of status.adapters, and one shared manager would make
// each apply remove the other's entry.
func cecFieldManager(machine string) string {
	return "equipment-operator-cec-" + machine
}

func ListCECBuses(c *Client) (*CECBusList, error) {
	list := &CECBusList{}
	if err := c.RequestJSON(http.MethodGet, cecBusesPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// watchCECBuses wakes a loop on every change to a CECBus. restarted
// is called for each watch opened again after the first.
func watchCECBuses(ctx context.Context, client *Client, resourceVersion string, wake chan<- struct{}, restarted func()) {
	watchCollection(ctx, client, cecBusesPath, resourceVersion, wake, restarted, func() (string, error) {
		list, err := ListCECBuses(client)
		if err != nil {
			return "", err
		}
		return list.Metadata.ResourceVersion, nil
	})
}

// applyCECBus sends one apply body under one field manager. force
// settles a conflict in the manager's favour, because each field these
// bodies state has one writer.
func applyCECBus(c *Client, path, manager string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	written := &CECBus{}
	return c.requestJSON(http.MethodPatch, path+"?fieldManager="+manager+"&force=true", applyContentType, encoded, written)
}

// cecBusApply is the identity every apply body carries.
func cecBusApply(name string) (string, string, ObjectMeta) {
	return equipmentAPIVersion, "CECBus", ObjectMeta{Name: name}
}

// ApplyCECAdapterStatus writes one machine's entry under
// status.adapters. The entry list is a map keyed by machine, so the
// apply touches that one entry. A nil entry removes the machine's
// entry, which a node workload does when its machine leaves a bus.
func ApplyCECAdapterStatus(c *Client, bus, machine string, entry *CECAdapterStatus) error {
	type status struct {
		Adapters []CECAdapterStatus `json:"adapters"`
	}
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     status     `json:"status"`
	}{Status: status{Adapters: []CECAdapterStatus{}}}
	body.APIVersion, body.Kind, body.Metadata = cecBusApply(bus)
	if entry != nil {
		body.Status.Adapters = append(body.Status.Adapters, *entry)
	}
	return applyCECBus(c, cecBusPath(bus)+"/status", cecFieldManager(machine), body)
}

// ApplyCECBusDerived writes what the Deployment derives: the merged
// device list and the conditions. It states no adapter entry.
func ApplyCECBusDerived(c *Client, bus string, devices []CECDevice, conditions []Condition) error {
	type status struct {
		Devices    []CECDevice `json:"devices,omitempty"`
		Conditions []Condition `json:"conditions,omitempty"`
	}
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Status     status     `json:"status"`
	}{Status: status{Devices: devices, Conditions: conditions}}
	body.APIVersion, body.Kind, body.Metadata = cecBusApply(bus)
	return applyCECBus(c, cecBusPath(bus)+"/status", fieldManager, body)
}

// discoveredCECLabelValue is the value of the discovered label on a
// CECBus the node workload made, which names the protocol it found.
const discoveredCECLabelValue = "cec"

// ApplyDiscoveredCECBus creates, or keeps, the CECBus a node workload
// makes for an adapter no CECBus names. It is in Listen, so the new
// adapter sends nothing until a person allows it, and it carries the
// discovered label, so the node workload deletes only its own objects.
func ApplyDiscoveredCECBus(c *Client, name, machine string) error {
	body := struct {
		APIVersion string     `json:"apiVersion"`
		Kind       string     `json:"kind"`
		Metadata   ObjectMeta `json:"metadata"`
		Spec       CECBusSpec `json:"spec"`
	}{Spec: CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: machine}}}}
	body.APIVersion, body.Kind, body.Metadata = cecBusApply(name)
	body.Metadata.Labels = map[string]string{discoveredLabel: discoveredCECLabelValue}
	return applyCECBus(c, cecBusPath(name), cecFieldManager(machine), body)
}

// DeleteCECBus removes one CECBus. A name that is already gone is not
// an error.
func DeleteCECBus(c *Client, name string) error {
	resp, err := c.send(context.Background(), http.MethodDelete, cecBusPath(name), "", nil)
	if err != nil {
		return err
	}
	defer drain(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("deleting CECBus %s: %s: %s", name, resp.Status, message)
	}
	return nil
}

// Display is the part of display-operator's Display this operator
// reads: the node the monitor is connected to, and the CEC physical
// address its EDID gives the machine's port.
type Display struct {
	Metadata ObjectMeta `json:"metadata"`
	Status   struct {
		Node            string `json:"node,omitempty"`
		PhysicalAddress string `json:"physicalAddress,omitempty"`
	} `json:"status"`
}

const displaysPath = "/apis/display.liken.sh/v1alpha1/displays"

func GetDisplay(c *Client, name string) (*Display, error) {
	display := &Display{}
	if err := c.RequestJSON(http.MethodGet, displaysPath+"/"+name, nil, display); err != nil {
		return nil, err
	}
	return display, nil
}
