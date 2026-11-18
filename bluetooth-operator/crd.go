package main

// The Adapter, Peripheral, and PairingRequest types of the
// bluetooth.liken.sh API. An Adapter is a radio and a Peripheral is a
// bonded device. A person pairs a device by creating a PairingRequest
// and unpairs it by deleting its Peripheral.
//
// Adapter and Peripheral are cluster-scoped, because a radio and its
// bonds belong to a machine's hardware and not to a tenant.
// PairingRequest is namespaced, so RBAC can let someone create
// PairingRequests in one namespace without letting them exec into the
// operator's pod.
//
// These structs have only the fields the operator reads or writes.
// Every write sends the resourceVersion from its read, so a concurrent
// writer gets a conflict and neither overwrites the other.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/liken-sh/liken/bluetooth-operator/bonds"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/conditions"
)

const (
	// The API group is the DRA driver's name, so it cannot collide with
	// another operator's group.
	pairingGroup   = DriverName
	pairingVersion = "v1alpha1"
	pairingAPI     = pairingGroup + "/" + pairingVersion
	pairingBase    = "/apis/" + pairingGroup + "/" + pairingVersion

	adapterKind        = "Adapter"
	peripheralKind     = "Peripheral"
	pairingRequestKind = "PairingRequest"
)

const (
	// adapterFinalizer stays on an Adapter while its radio is present.
	// Garbage collection of a deleted Adapter deletes its Peripherals and
	// their bond Secrets, which would unpair every device of a radio
	// that is still in use. While the finalizer stays, the Adapter is not
	// removed, and status.deletionRefused gives the reason.
	adapterFinalizer = DriverName + "/radio-present"

	// peripheralFinalizer keeps a deleted Peripheral until the unpair
	// teardown in unpair.go has run.
	peripheralFinalizer = DriverName + "/unpair"
)

// The phases of a PairingRequest. A request is Open while its window
// runs. It becomes Paired when the approved device pairs, or Expired
// when the window closes with no approval. The operator does not retry
// a finished request, because by then the device's own pairing mode
// has timed out too.
const (
	phaseOpen    = "Open"
	phasePaired  = "Paired"
	phaseExpired = "Expired"
)

const (
	// defaultWindowSeconds gives a person time to put the controller in
	// pairing mode, read status.seen, and write the address into
	// spec.device.
	defaultWindowSeconds = 180

	// defaultTTLSeconds keeps a finished request for one day, long
	// enough to read it the next morning.
	defaultTTLSeconds = 86400

	// maxSeenDevices and maxSeenNameBytes limit the size of status.seen,
	// because every device in radio range adds an entry. 64 bytes is
	// the limit that the API server puts on a ResourceSlice string
	// attribute, and attributeString cuts to the same length.
	maxSeenDevices   = 16
	maxSeenNameBytes = 64
)

// ObjectMeta has the metadata fields that the operator reads or writes.
// A status write does not change Generation, so editwatch.go compares
// it to tell a person's edit of a spec from the operator's own status
// write.
type ObjectMeta struct {
	Name              string            `json:"name,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Generation        int64             `json:"generation,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Finalizers        []string          `json:"finalizers,omitempty"`
	OwnerReferences   []OwnerReference  `json:"ownerReferences,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
}

// deleting reports whether someone has deleted this object and the API
// server keeps it only for its finalizers.
func (m ObjectMeta) deleting() bool { return m.DeletionTimestamp != "" }

// hasFinalizer reports whether the object has the finalizer.
func (m ObjectMeta) hasFinalizer(finalizer string) bool {
	for _, name := range m.Finalizers {
		if name == finalizer {
			return true
		}
	}
	return false
}

// withFinalizer returns the finalizer list with finalizer added.
// Neither withFinalizer nor withoutFinalizer changes m.Finalizers, so
// when the patch fails, the caller's copy still matches the server's.
func (m ObjectMeta) withFinalizer(finalizer string) []string {
	if m.hasFinalizer(finalizer) {
		return m.Finalizers
	}
	return append(append([]string{}, m.Finalizers...), finalizer)
}

func (m ObjectMeta) withoutFinalizer(finalizer string) []string {
	kept := []string{}
	for _, name := range m.Finalizers {
		if name != finalizer {
			kept = append(kept, name)
		}
	}
	return kept
}

// Adapter is one Bluetooth radio. The operator creates it for the radio
// that its pod claimed, and names it after the radio's address.
//
// The Adapter owns its Peripherals, and each Peripheral owns its bond
// Secret. No name or spec field of these objects identifies a machine,
// so a USB radio that moves to another machine keeps its Adapter,
// Peripherals, and Secrets. status.node reports the machine it is on.
type Adapter struct {
	APIVersion string        `json:"apiVersion,omitempty"`
	Kind       string        `json:"kind,omitempty"`
	Metadata   ObjectMeta    `json:"metadata"`
	Spec       AdapterSpec   `json:"spec"`
	Status     AdapterStatus `json:"status,omitempty"`
}

type AdapterSpec struct {
	// Alias is the value for BlueZ's Adapter1.Alias, the name the radio
	// advertises for itself.
	Alias string `json:"alias,omitempty"`

	// Privacy is the value of bluetoothd's Privacy key for this radio.
	// Empty means off. bluetoothd reads the key only when it starts, so
	// the operator replaces its own pod to apply a change (privacy.go).
	Privacy string `json:"privacy,omitempty"`

	// Btmon turns on a btmon trace of this radio. The operator writes
	// the value into the pod's settings volume, and the btmon container
	// starts or stops btmon when it changes (btmon.go).
	Btmon bool `json:"btmon,omitempty"`
}

type AdapterStatus struct {
	Address string `json:"address,omitempty"`
	Node    string `json:"node,omitempty"`
	Powered bool   `json:"powered"`

	// Privacy is the value that the running bluetoothd started with,
	// read from the pod's settings volume. It is what bluetoothd was
	// given, not what the kernel accepted.
	Privacy string `json:"privacy,omitempty"`

	// Btmon is the value in the btmon file of the settings volume, which
	// the btmon container reads. It is a pointer so that sameStatus finds
	// a stored status with no btmon field different from false, and the
	// operator writes the field. It has no omitempty, so the printer
	// column shows false and not an empty cell.
	Btmon *bool `json:"btmon"`

	// DeletionRefused is why the operator kept its finalizer on an
	// Adapter that someone deleted. It is empty otherwise.
	DeletionRefused string `json:"deletionRefused,omitempty"`
}

// Peripheral is one device that has a bond with one adapter. The
// operator creates it when a pairing succeeds, and when bluetoothd
// already has a bond that no Peripheral records. It owns the Secret
// that stores the bond's keys.
//
// Deleting a Peripheral unpairs the device, so the operator never
// deletes one on its own. When bluetoothd loses the bond, the
// Peripheral stays and status.bond.held is false, and a person decides
// whether to unpair.
type Peripheral struct {
	APIVersion string           `json:"apiVersion,omitempty"`
	Kind       string           `json:"kind,omitempty"`
	Metadata   ObjectMeta       `json:"metadata"`
	Spec       PeripheralSpec   `json:"spec"`
	Status     PeripheralStatus `json:"status,omitempty"`
}

type PeripheralSpec struct {
	// Alias is the value for Device1.Alias. bluetoothd stores the alias
	// in the bond's info file, so the Secret holds it with the keys.
	Alias string `json:"alias,omitempty"`

	// Trusted is the value for Device1.Trusted, which lets the device
	// connect without an agent's approval. It is a pointer because the
	// CRD defaults it to true: a plain bool with omitempty would drop
	// false from the JSON, and the API server would default it to true.
	Trusted *bool `json:"trusted,omitempty"`
}

type PeripheralStatus struct {
	Address string `json:"address,omitempty"`

	// Name is the name the device reports for itself, and Icon is the
	// freedesktop icon name that BlueZ derives for it. Both are empty
	// when bluetoothd has no object for the device.
	Name string `json:"name,omitempty"`
	Icon string `json:"icon,omitempty"`

	Adapter string `json:"adapter,omitempty"`
	Node    string `json:"node,omitempty"`

	Bond BondStatus `json:"bond,omitempty"`

	// Battery is nil when the device reports no level. Most controllers
	// have no battery, and a controller that has one reports a level
	// only while it is connected.
	Battery *BatteryStatus `json:"battery,omitempty"`

	Conditions []Condition `json:"conditions,omitempty"`
}

// BondStatus is what the operator observes about the bond.
type BondStatus struct {
	// Held is whether bluetoothd still has the bond.
	Held bool `json:"held"`

	// Paired, Bonded, Trusted, and Connected are BlueZ's Device1
	// properties, read on every pass, so a person can debug a device that
	// does not pair or reconnect without running bluetoothctl in the pod.
	Paired    bool `json:"paired"`
	Bonded    bool `json:"bonded"`
	Trusted   bool `json:"trusted"`
	Connected bool `json:"connected"`

	Secret   string `json:"secret,omitempty"`
	PairedAt string `json:"pairedAt,omitempty"`
	Request  string `json:"request,omitempty"`
}

// BatteryStatus is the charge level the device reports. The operator
// reads it from the kernel's power supply class when the kernel
// registers one, and from BlueZ's org.bluez.Battery1 interface when it
// does not.
//
// Source is where the level came from. For a kernel reading it is the
// power supply's name, such as ps-controller-battery-<address>. For a
// BlueZ reading it is BlueZ's own source name, such as HID or a GATT
// service.
//
// Charging is absent for a BlueZ reading, because Battery1 reports a
// level and not a direction, and for a kernel reading whose status is
// Unknown.
type BatteryStatus struct {
	Percentage int    `json:"percentage"`
	Source     string `json:"source,omitempty"`
	Charging   *bool  `json:"charging,omitempty"`
}

// Condition is the Kubernetes condition type from kubernetes/conditions,
// which every liken component writes.
type Condition = conditions.Condition

// PairingRequest runs discovery on one adapter for the length of its
// window and pairs the device whose address a person writes into
// spec.device. The operator never pairs a device on its own, because
// the first device to respond could be a stranger's.
type PairingRequest struct {
	APIVersion string               `json:"apiVersion,omitempty"`
	Kind       string               `json:"kind,omitempty"`
	Metadata   ObjectMeta           `json:"metadata"`
	Spec       PairingRequestSpec   `json:"spec"`
	Status     PairingRequestStatus `json:"status,omitempty"`
}

type PairingRequestSpec struct {
	Adapter                 string `json:"adapter"`
	WindowSeconds           int    `json:"windowSeconds,omitempty"`
	Device                  string `json:"device,omitempty"`
	TTLSecondsAfterFinished *int   `json:"ttlSecondsAfterFinished,omitempty"`
}

func (s PairingRequestSpec) window() time.Duration {
	seconds := s.WindowSeconds
	if seconds <= 0 {
		seconds = defaultWindowSeconds
	}
	return time.Duration(seconds) * time.Second
}

func (s PairingRequestSpec) ttl() time.Duration {
	seconds := defaultTTLSeconds
	if s.TTLSecondsAfterFinished != nil {
		seconds = *s.TTLSecondsAfterFinished
	}
	if seconds < 0 {
		seconds = 0
	}
	return time.Duration(seconds) * time.Second
}

type PairingRequestStatus struct {
	Phase          string       `json:"phase,omitempty"`
	WindowClosesAt string       `json:"windowClosesAt,omitempty"`
	Seen           []SeenDevice `json:"seen,omitempty"`

	// SeenTruncated is true when the radio observed more devices during
	// this window than status.seen lists.
	SeenTruncated bool `json:"seenTruncated"`

	Peripheral string `json:"peripheral,omitempty"`

	// FinishedAt is when the request reached Paired or Expired, and the
	// TTL counts from it.
	FinishedAt string `json:"finishedAt,omitempty"`

	// Message explains why a request did not do what was asked, for
	// example when bluetoothd refused a Pair call. It is empty
	// otherwise.
	Message string `json:"message,omitempty"`
}

func (s PairingRequestStatus) finished() bool {
	return s.Phase == phasePaired || s.Phase == phaseExpired
}

// SeenDevice is a device that the radio observed during the window and
// that a person can approve.
type SeenDevice struct {
	Address   string `json:"address"`
	Name      string `json:"name,omitempty"`
	FirstSeen string `json:"firstSeen,omitempty"`
}

func adaptersPath() string { return pairingBase + "/adapters" }

func adapterPath(name string) string { return adaptersPath() + "/" + name }

func peripheralsPath() string { return pairingBase + "/peripherals" }

func peripheralPath(name string) string { return peripheralsPath() + "/" + name }

// pairingRequestsPath is the collection across all namespaces, because
// the operator serves requests in every namespace.
func pairingRequestsPath() string { return pairingBase + "/pairingrequests" }

func pairingRequestPath(namespace, name string) string {
	return pairingBase + "/namespaces/" + namespace + "/pairingrequests/" + name
}

// byAdapter adds a label selector to path, so that a list returns only
// the Peripherals or bond Secrets of one radio, and the operator never
// writes another radio's objects. Both kinds carry the adapter in a
// label, because a selector cannot match part of a name.
func byAdapter(path, adapterKey string) string {
	query := url.Values{}
	query.Set("labelSelector", adapterSelector(adapterKey))
	return path + "?" + query.Encode()
}

func adapterSelector(adapterKey string) string {
	return bonds.AdapterLabel + "=" + adapterKey
}

// createObject posts object to collection and returns the copy that the
// server stored. That copy has the UID that an owner reference needs.
func createObject[T any](c *apiclient.Client, collection string, object *T) (*T, error) {
	body, err := json.Marshal(object)
	if err != nil {
		return nil, err
	}
	created := new(T)
	if err := c.RequestJSON(http.MethodPost, collection, body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// deleteObject deletes the object at path, and returns no error when
// the object is already gone.
func deleteObject(c *apiclient.Client, path string) error {
	err := c.RequestJSON(http.MethodDelete, path, nil, nil)
	if err == apiclient.ErrNotFound {
		return nil
	}
	return err
}

// The operator edits a finalizer list with a JSON merge patch, because
// a replace of the whole object would drop every field these structs
// do not model, such as a person's annotations. The patch includes the
// resourceVersion, so it fails with a conflict like a replace does.
const mergePatchType = "application/merge-patch+json"

// patchFinalizers replaces an object's finalizer list and returns the
// new resourceVersion, which a later write in the same pass must send
// to avoid a conflict.
func patchFinalizers(c *apiclient.Client, path, resourceVersion string, finalizers []string) (string, error) {
	patch := map[string]any{
		"metadata": map[string]any{
			"resourceVersion": resourceVersion,
			"finalizers":      finalizers,
		},
	}
	body, err := json.Marshal(patch)
	if err != nil {
		return "", err
	}
	var patched struct {
		Metadata ObjectMeta `json:"metadata"`
	}
	if err := c.Request(http.MethodPatch, path, mergePatchType, body, &patched); err != nil {
		return "", err
	}
	return patched.Metadata.ResourceVersion, nil
}

// timestamp formats a time the way the API server does: RFC 3339, in
// UTC, in whole seconds.
func timestamp(at time.Time) string { return at.UTC().Format(time.RFC3339) }

// parseTimestamp returns the zero time for text that does not parse.
// Each caller then writes the field again from the current time, so a
// status field that a person cleared or edited by hand cannot stall a
// request.
func parseTimestamp(text string) time.Time {
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}
	}
	return at
}
