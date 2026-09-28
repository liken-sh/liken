package main

// The API calls for CECBus, and the reads of the Displays. Each write
// is a server-side apply that states only the fields its writer owns.
// Three kinds of writer share one CECBus: the person who writes the
// spec, the node workload on each adapter's machine, which writes its
// own entry under status.adapters, and the Deployment, which writes
// status.devices and the conditions. Each one applies under its own
// field manager, so no writer removes a field another owns.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
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

// readCECBuses answers every CECBus from the watch's store, and lists
// them from the API server while the store has nothing to give
// (objectcache.go).
func readCECBuses(c *Client, held *watchStore) (*CECBusList, error) {
	if view := held.view(); view.ready() {
		items, err := currentList[CECBus](c, heldObjects{view: view, versions: c.versions.cecBuses}, cecBusPath)
		return &CECBusList{Items: items}, err
	}
	return ListCECBuses(c)
}

// watchCECBuses wakes a loop on every change to a CECBus, its status
// included: the Deployment derives its status from the entries the node
// workloads write, and a node workload reads the Deployment's. restarted
// is called for each watch opened again after the first.
func watchCECBuses(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), held *watchStore) {
	watchCollection(ctx, client, cecBusResource, "", wakeOnEvery(wake), func() { poke(wake) }, restarted, held)
}

// applyCECBus sends one apply body under one field manager. force
// settles a conflict in the manager's favour, because each field these
// bodies state has one writer.
func applyCECBus(c *Client, name, path, manager string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = written[CECBus](c.versions.cecBuses, name, func() (*CECBus, error) {
		answer := &CECBus{}
		return answer, c.requestJSON(http.MethodPatch, path+"?fieldManager="+manager+"&force=true", applyContentType, encoded, answer)
	})
	return err
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
	return applyCECBus(c, bus, cecBusPath(bus)+"/status", cecFieldManager(machine), body)
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
	return applyCECBus(c, bus, cecBusPath(bus)+"/status", fieldManager, body)
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
	return applyCECBus(c, name, cecBusPath(name), cecFieldManager(machine), body)
}

// DeleteCECBus removes one CECBus. A name that is already gone is not
// an error.
func DeleteCECBus(c *Client, name string) error {
	return c.versions.cecBuses.send(name, func() (string, error) {
		return "", deleteObject(c, cecBusPath(name), "CECBus "+name)
	})
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
	return get[Display](c, displayPath(name))
}

func displayPath(name string) string { return displaysPath + "/" + name }

// readDisplays answers every Display from the watch's store, and lists
// them from the API server while the store has nothing to give. This
// operator writes no Display, so the read has no memo.
func readDisplays(c *Client, held *watchStore) (*DisplayList, error) {
	if view := held.view(); view.ready() {
		items, err := currentList[Display](c, heldObjects{view: view}, displayPath)
		return &DisplayList{Items: items}, err
	}
	return ListDisplays(c)
}

// readDisplay answers one Display from the watch's store. A store that
// holds every Display answers ErrNotFound for one it does not hold,
// once it holds its first read. The Display is read from the API server
// while the store has nothing to give, when its copy does not convert,
// and when the store holds the Displays of one machine and not this
// one, such as a Display that a bus names on another machine.
func readDisplay(c *Client, held *watchStore, name string) (*Display, error) {
	view := held.view()
	if display, ok := cachedCopy[Display](view, name); ok {
		return display, nil
	}
	if view.ready() && view.whole {
		if _, stored, _ := view.store.GetByKey(name); !stored {
			return nil, ErrNotFound
		}
	}
	return GetDisplay(c, name)
}

// watchDisplays wakes a loop when a Display appears, goes, or moves to
// another node or physical address, which is all a Television reads of
// it. display-operator writes other status fields of a Display, and
// those writes wake nothing.
func watchDisplays(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), held *watchStore) {
	displays := markHandler[Display, displayPlace]{what: "the Displays", wake: wake, mark: displayMark}
	watchCollection(ctx, client, displayResource, "", displays.handler(), func() { poke(wake) }, restarted, held)
}

// nodeDisplays answers the node workload's Display watch on one
// machine, which wakes the loop on every change to a Display whose
// status.node is that machine. The adapter reads the Display on its own
// machine, and the Display CRD declares status.node a selectable
// field, so the API server sends no other machine's Display writes.
func nodeDisplays(machine string) func(context.Context, *Client, chan<- struct{}, func(), *watchStore) {
	return func(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), held *watchStore) {
		watchCollection(ctx, client, displayResource, displayNodeSelector(machine), wakeOnEvery(wake), func() { poke(wake) }, restarted, held)
	}
}

// displayNodeSelector is the field selector that takes the Displays
// whose status.node is one machine.
func displayNodeSelector(machine string) string {
	return "status.node=" + machine
}

// displayPlace is the part of a Display that a Television reads, and
// the Display's UID. After a gap in the watch, a Display deleted and
// created again with the same name reaches the handler as an update,
// and the UID tells the two apart.
type displayPlace struct {
	uid, node, physicalAddress string
}

func displayMark(display Display) displayPlace {
	return displayPlace{uid: display.Metadata.UID, node: display.Status.Node, physicalAddress: display.Status.PhysicalAddress}
}

// watchReceiverSpecs wakes a loop when a Receiver appears, goes, or
// changes its spec. A Television reads a Receiver's spec.inputs and no
// status, and a Receiver's status moves with every volume step, so a
// status write wakes nothing.
func watchReceiverSpecs(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), held *watchStore) {
	receivers := markHandler[Receiver, specMark]{what: "the Receivers", wake: wake, mark: receiverSpecMark}
	watchCollection(ctx, client, receiverResource, "", receivers.handler(), func() { poke(wake) }, restarted, held)
}

// specMark is an object's metadata.generation, which the API server
// moves on each spec change and on no status change, and its UID. After
// a gap in the watch, an object deleted and created again with the same
// name reaches the handler as an update, and the new object can have
// the same generation as the old one.
type specMark struct {
	uid        string
	generation int64
}

func receiverSpecMark(receiver Receiver) specMark {
	return specMark{uid: receiver.Metadata.UID, generation: receiver.Metadata.Generation}
}

type DisplayList struct {
	Metadata ListMeta  `json:"metadata"`
	Items    []Display `json:"items"`
}

// ListDisplays reads every Display. A cluster without display-operator
// has no Display definition, and the API server answers the list with
// not found; that is a cluster with no Display, not a failure.
func ListDisplays(c *Client) (*DisplayList, error) {
	return listDisplays(c, displaysPath)
}

// ListDisplaysOn reads the Displays whose status.node is one machine.
func ListDisplaysOn(c *Client, machine string) (*DisplayList, error) {
	return listDisplays(c, displaysPath+"?fieldSelector="+url.QueryEscape(displayNodeSelector(machine)))
}

func listDisplays(c *Client, path string) (*DisplayList, error) {
	list := &DisplayList{}
	err := c.RequestJSON(http.MethodGet, path, nil, list)
	if err == ErrNotFound {
		return &DisplayList{}, nil
	}
	if err != nil {
		return nil, err
	}
	return list, nil
}
