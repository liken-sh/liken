package main

// API calls for CECBus, and reads of Display. A person writes a
// CECBus's spec, the node workload on each adapter's machine writes its
// own entry in status.adapters, and the Deployment writes status.devices
// and the conditions. Each workload writes with a server-side apply
// under its own field manager, so no writer removes a field that another
// owns.

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

// CECBus is cluster-scoped, so its path has no namespace.
const cecBusesPath = "/apis/" + equipmentAPIVersion + "/cecbuses"

func cecBusPath(name string) string {
	return cecBusesPath + "/" + name
}

// cecFieldManager is the field manager of the node workload on one
// machine. Each node workload owns one entry of status.adapters, so with
// one shared manager each apply would remove the other machines'
// entries.
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

// readCECBuses returns every CECBus from the watch's store, or lists
// them from the API server while the store is empty (objectcache.go).
func readCECBuses(c *Client, held *watchStore) (*CECBusList, error) {
	items, err := informer.List[CECBus](c.Client, informer.Held{View: held.view(), Versions: c.versions.cecBuses}, cecBusesPath, cecBusPath)
	return &CECBusList{Items: items}, err
}

// watchCECBuses wakes the loop on every change to a CECBus, status
// included. The Deployment derives its status from the entries that the
// node workloads write, and a node workload reads the status that the
// Deployment writes. restarted is called each time the watch reopens
// after the first open.
func watchCECBuses(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), held *watchStore) {
	watchCollection(ctx, client, informer.Source{Resource: cecBusResource}, nil, wakeOnEvery(wake), func() { sendWake(wake) }, restarted, held)
}

// applyCECBus sends one apply body under one field manager. It forces
// the apply because each field in these bodies has one writer, which
// takes the field over from any other manager that set it.
func applyCECBus(c *Client, name, path, manager string, body any) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, err = memo.Written[CECBus](c.versions.cecBuses, name, func() (*CECBus, error) {
		answer := &CECBus{}
		return answer, c.Request(http.MethodPatch, path+"?fieldManager="+manager+"&force=true", applyContentType, encoded, answer)
	})
	return err
}

func cecBusApply(name string) (string, string, ObjectMeta) {
	return equipmentAPIVersion, "CECBus", ObjectMeta{Name: name}
}

// ApplyCECAdapterStatus writes one machine's entry in status.adapters.
// The CRD declares the list as a map keyed by machine, so the apply
// changes only that entry. A nil entry removes it, which a node workload
// does when its machine leaves a bus.
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

// ApplyCECBusDerived writes status.devices and the conditions, which
// the Deployment computes from the adapter entries.
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
// CECBus that a node workload created.
const discoveredCECLabelValue = "cec"

// CreateDiscoveredCECBus creates a CECBus in Listen mode for an adapter
// that no CECBus lists, so the adapter sends nothing until a person
// changes the mode. The discovered label lets the node workload delete
// only the buses it created. It is a create and not a forced apply,
// because a create for an existing name fails with apiclient.ErrConflict
// and changes nothing, so a bus that a person created under that name is
// never overwritten.
func CreateDiscoveredCECBus(c *Client, name, machine string) error {
	encoded, err := json.Marshal(CECBus{
		APIVersion: equipmentAPIVersion,
		Kind:       "CECBus",
		Metadata:   ObjectMeta{Name: name, Labels: map[string]string{discoveredLabel: discoveredCECLabelValue}},
		Spec:       CECBusSpec{Mode: CECListen, Adapters: []CECBusAdapter{{Machine: machine}}},
	})
	if err != nil {
		return err
	}
	_, err = memo.Written[CECBus](c.versions.cecBuses, name, func() (*CECBus, error) {
		answer := &CECBus{}
		return answer, c.RequestJSON(http.MethodPost, cecBusesPath+"?fieldManager="+cecFieldManager(machine), encoded, answer)
	})
	return err
}

// DeleteCECBus deletes one CECBus. A CECBus that is already gone is not
// an error.
func DeleteCECBus(c *Client, name string) error {
	return c.versions.cecBuses.Send(name, func() (string, error) {
		return "", deleteObject(c, cecBusPath(name), "CECBus "+name)
	})
}

// Display is the part of display-operator's Display that this operator
// reads: the node the monitor is connected to, and the CEC physical
// address that the monitor's EDID gives that machine's port.
type Display struct {
	Metadata ObjectMeta `json:"metadata"`
	Status   struct {
		Node            string `json:"node,omitempty"`
		PhysicalAddress string `json:"physicalAddress,omitempty"`
	} `json:"status"`
}

const displaysPath = "/apis/display.liken.sh/v1alpha1/displays"

func GetDisplay(c *Client, name string) (*Display, error) {
	return apiclient.Get[Display](c.Client, displayPath(name))
}

func displayPath(name string) string { return displaysPath + "/" + name }

// readDisplays returns every Display from the watch's store, or lists
// them from the API server while the store is empty. It passes no
// written versions, because this operator never writes a Display. On a
// cluster without display-operator the Display CRD does not exist, and
// the list's not found returns an empty list.
func readDisplays(c *Client, held *watchStore) (*DisplayList, error) {
	items, err := informer.List[Display](c.Client, informer.Held{View: held.view()}, displaysPath, displayPath)
	if errors.Is(err, apiclient.ErrNotFound) {
		return &DisplayList{}, nil
	}
	return &DisplayList{Items: items}, err
}

// readDisplay returns one Display from the watch's store. A name missing
// from the store returns apiclient.ErrNotFound when the store has
// finished its first list and holds every Display, and is read from the
// API server otherwise. A stored copy that does not convert is read from
// the API server too. The node workload's store holds only its own machine's
// Displays, so it reads a Display on another machine that a bus lists
// this way.
func readDisplay(c *Client, held *watchStore, name string) (*Display, error) {
	view := held.view()
	if display, ok := informer.Cached[Display](view, name); ok {
		return display, nil
	}
	if view.Ready() && view.Whole {
		if _, stored, _ := view.Store.GetByKey(name); !stored {
			return nil, apiclient.ErrNotFound
		}
	}
	return GetDisplay(c, name)
}

// watchDisplays wakes the loop when a Display is created, deleted, or
// changes its node or physical address, which are the fields a
// Television reads. On a cluster without display-operator the Display
// CRD does not exist, and the watch has no Displays until it does.
func watchDisplays(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), held *watchStore) {
	displays := markHandler[Display, displayPlace]{what: "the Displays", wake: wake, mark: displayMark}
	watchCollection(ctx, client, informer.Source{Resource: displayResource}, apierrors.IsNotFound, displays.handler(), func() { sendWake(wake) }, restarted, held)
}

// nodeDisplays returns the Display watch of the node workload on one
// machine, which wakes the loop on every change to a Display on that
// machine. The Display CRD declares status.node as a selectable field,
// so the API server sends no other machine's Displays. When the CRD does
// not declare that field, the API server rejects the selector, the store
// stays empty, and readDisplay reads the adapter's Display from the API
// server.
func nodeDisplays(machine string) func(context.Context, *Client, chan<- struct{}, func(), *watchStore) {
	return func(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), held *watchStore) {
		watchCollection(ctx, client, informer.Source{Resource: displayResource, FieldSelector: displayNodeSelector(machine)}, displaysUnselectable, wakeOnEvery(wake), func() { sendWake(wake) }, restarted, held)
	}
}

// displaysUnselectable reports whether a Display watch error means
// there is nothing to watch: the Display definition is missing, or it
// does not declare status.node as selectable.
func displaysUnselectable(err error) bool {
	return apierrors.IsNotFound(err) || apierrors.IsBadRequest(err)
}

func displayNodeSelector(machine string) string {
	return "status.node=" + machine
}

// displayPlace is the part of a Display that a Television reads, and
// its UID. After a gap in the watch, a Display that was deleted and
// created again under the same name arrives as an update, and only the
// UID tells it from the old one.
type displayPlace struct {
	uid, node, physicalAddress string
}

func displayMark(display Display) displayPlace {
	return displayPlace{uid: display.Metadata.UID, node: display.Status.Node, physicalAddress: display.Status.PhysicalAddress}
}

// watchReceiverSpecs wakes the loop when a Receiver is created,
// deleted, or changes its spec. A Television reads only a Receiver's
// spec.inputs, and a Receiver's status changes on every volume step, so
// status writes do not wake the loop.
func watchReceiverSpecs(ctx context.Context, client *Client, wake chan<- struct{}, restarted func(), held *watchStore) {
	receivers := markHandler[Receiver, specMark]{what: "the Receivers", wake: wake, mark: receiverSpecMark}
	watchCollection(ctx, client, informer.Source{Resource: receiverResource}, nil, receivers.handler(), func() { sendWake(wake) }, restarted, held)
}

// specMark is an object's metadata.generation and UID. The API server
// increments the generation on each spec change and on no status
// change. The UID tells a recreated object from the old one, as in
// displayPlace, because the new object can have the same generation.
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
