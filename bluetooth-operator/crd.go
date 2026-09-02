package main

// The pairing API: three custom resources that let a person create,
// read, and delete a bond with kubectl.
//
// One rule sets what becomes an object and what stays status. A
// spec is desired state, a status is observed state, and a radio
// session is never an object. So the Adapter and the Peripheral are the
// durable inventory, the PairingRequest is the act of pairing, and
// everything the radio reports about them goes in status.
//
// Adapter and Peripheral are cluster-scoped, because a radio and a bond
// belong to a machine's hardware and not to a tenant, and because the
// ResourceSlice that publishes the same controllers is cluster-scoped
// for the same reason. PairingRequest is namespaced, so that RBAC can
// grant "may create PairingRequests" in one namespace without granting
// exec into the operator's pod. This API exists to make that narrower
// grant possible.
//
// Like the ResourceSlice and Secret types in this repository, these
// structs hold only the fields the operator reads or writes. The
// objects also have annotations, generation, and everything else an
// object has, and none of that changes what a pairing needs. The one
// thing every write does include is the resourceVersion from its read,
// so a second writer gets a conflict instead of losing the first
// writer's change.

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/liken-sh/bluetooth-operator/bonds"
)

const (
	// The API group and version for these objects. The group is the
	// driver's own name, so the objects sort beside the slices this
	// operator publishes and no other operator's resources can collide
	// with them.
	pairingGroup   = DriverName
	pairingVersion = "v1alpha1"
	pairingAPI     = pairingGroup + "/" + pairingVersion
	pairingBase    = "/apis/" + pairingGroup + "/" + pairingVersion

	adapterKind        = "Adapter"
	peripheralKind     = "Peripheral"
	pairingRequestKind = "PairingRequest"
)

const (
	// adapterFinalizer holds an Adapter object while the radio it names is
	// present. Deleting an Adapter cascades to every Peripheral under it
	// and to every bond Secret under those, so a delete against live
	// hardware would be a mass unpair. With the finalizer, the delete
	// stays pending instead, and the reason goes into the status.
	adapterFinalizer = DriverName + "/radio-present"

	// peripheralFinalizer holds a Peripheral while its teardown runs.
	// Deleting a Peripheral is the unpair API, and an unpair has an order:
	// end the session, let the claim that holds the controller release it,
	// retire the device from the slice, and only then remove the bond from
	// bluetoothd.
	peripheralFinalizer = DriverName + "/unpair"
)

// The phases a PairingRequest reports. A request is Open while its
// window is running, and it reaches exactly one of the other two: it
// paired the device a person approved, or the window closed with no
// approval. The operator retries neither end state, because the
// device's own pairing mode has timed out by then as well.
const (
	phaseOpen    = "Open"
	phasePaired  = "Paired"
	phaseExpired = "Expired"
)

const (
	// defaultWindowSeconds is how long a window runs when the request
	// states no length. Three minutes is long enough to hold the
	// controller's buttons, read status.seen, and write the address back.
	defaultWindowSeconds = 180

	// defaultTTLSeconds is how long a finished request stays before the
	// operator deletes it. One day is the Job convention, and it is long
	// enough to read the request the next morning. The Peripheral records
	// which request produced it, so that record outlasts the deletion.
	defaultTTLSeconds = 86400

	// maxSeenDevices and maxSeenNameBytes bound status.seen. The list is
	// written from radio observations, so a busy room would otherwise
	// grow the object without limit. The name limit is the same 64 bytes
	// the ResourceSlice puts on a string attribute.
	maxSeenDevices   = 16
	maxSeenNameBytes = 64
)

// ObjectMeta is the identity every one of these objects has.
//
// Finalizers and DeletionTimestamp are here because both controllers
// are finalizer controllers: an object that is deleting keeps its
// deletionTimestamp and stays readable until the last finalizer is
// removed, and that window is where the ordered teardown runs.
type ObjectMeta struct {
	Name              string            `json:"name,omitempty"`
	Namespace         string            `json:"namespace,omitempty"`
	UID               string            `json:"uid,omitempty"`
	ResourceVersion   string            `json:"resourceVersion,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Finalizers        []string          `json:"finalizers,omitempty"`
	OwnerReferences   []OwnerReference  `json:"ownerReferences,omitempty"`
	CreationTimestamp string            `json:"creationTimestamp,omitempty"`
	DeletionTimestamp string            `json:"deletionTimestamp,omitempty"`
}

// deleting reports whether somebody has requested this object's
// deletion. The API server refuses to remove an object that has a
// finalizer, and writes this field instead. A teardown starts when
// this field is set.
func (m ObjectMeta) deleting() bool { return m.DeletionTimestamp != "" }

// holds reports whether this object has the named finalizer.
func (m ObjectMeta) holds(finalizer string) bool {
	for _, held := range m.Finalizers {
		if held == finalizer {
			return true
		}
	}
	return false
}

// with returns the finalizer list with one added, and without returns
// it with one removed. Both return a new slice, because the caller's
// copy is the object it read and a patch that failed must leave that
// copy as the server has it.
func (m ObjectMeta) with(finalizer string) []string {
	if m.holds(finalizer) {
		return m.Finalizers
	}
	return append(append([]string{}, m.Finalizers...), finalizer)
}

func (m ObjectMeta) without(finalizer string) []string {
	kept := []string{}
	for _, held := range m.Finalizers {
		if held != finalizer {
			kept = append(kept, held)
		}
	}
	return kept
}

// Adapter is the cluster's record of one Bluetooth radio. The operator
// creates it for the radio its pod claimed, and names it for that
// radio's address in the form a Kubernetes name accepts.
//
// The Adapter is the root of the ownership tree: Peripherals belong to
// it, and each Peripheral owns its bond Secret. Nothing in that tree
// names a machine, so a dongle carried to another machine keeps its
// Adapter, its Peripherals, and their Secrets. The radio's current
// location is reported in status.
type Adapter struct {
	APIVersion string        `json:"apiVersion,omitempty"`
	Kind       string        `json:"kind,omitempty"`
	Metadata   ObjectMeta    `json:"metadata"`
	Spec       AdapterSpec   `json:"spec"`
	Status     AdapterStatus `json:"status,omitempty"`
}

type AdapterSpec struct {
	// Alias reconciles into BlueZ's Adapter1.Alias, which is the name
	// the radio broadcasts about itself.
	Alias string `json:"alias,omitempty"`
}

type AdapterStatus struct {
	Address string `json:"address,omitempty"`
	Node    string `json:"node,omitempty"`
	Powered bool   `json:"powered"`

	// DeletionRefused names why the operator kept its finalizer on an
	// Adapter somebody deleted. It is empty at every other time.
	DeletionRefused string `json:"deletionRefused,omitempty"`
}

// Peripheral is the durable fact that one device holds a bond with one
// adapter. The operator creates it when a pairing succeeds and when it
// adopts a bond that bluetoothd already held, and it owns the Secret
// that holds that one bond.
//
// Deleting a Peripheral is the unpair API. Nothing else deletes one: a
// Peripheral whose bond disappeared from bluetoothd keeps its object
// and reports the gap in status, because deletion means unpair and a
// person decides that.
type Peripheral struct {
	APIVersion string           `json:"apiVersion,omitempty"`
	Kind       string           `json:"kind,omitempty"`
	Metadata   ObjectMeta       `json:"metadata"`
	Spec       PeripheralSpec   `json:"spec"`
	Status     PeripheralStatus `json:"status,omitempty"`
}

type PeripheralSpec struct {
	// Alias reconciles into Device1.Alias. bluetoothd stores the alias
	// in the bond's own info file, so the name is stored in the Secret
	// with the keys.
	Alias string `json:"alias,omitempty"`

	// Trusted reconciles into Device1.Trusted, which lets the device
	// reconnect on its own. It is a pointer because the CRD
	// defaults it to true, and a plain false would be indistinguishable
	// from an unset field on the wire.
	Trusted *bool `json:"trusted,omitempty"`
}

type PeripheralStatus struct {
	Address string `json:"address,omitempty"`

	// Name is the name the device reports for itself, and Icon is the
	// freedesktop icon name BlueZ derives for it. Both are empty for a
	// device bluetoothd holds no object for.
	Name string `json:"name,omitempty"`
	Icon string `json:"icon,omitempty"`

	Adapter string `json:"adapter,omitempty"`
	Node    string `json:"node,omitempty"`

	Bond BondStatus `json:"bond,omitempty"`

	// Battery is nil when the device reports no level. Most controllers
	// have no battery, and one that has a battery reports a level only
	// while it is connected.
	Battery *BatteryStatus `json:"battery,omitempty"`

	Conditions []Condition `json:"conditions,omitempty"`
}

// BondStatus is what the operator observes about the bond itself.
type BondStatus struct {
	// Held reports whether bluetoothd still holds the bond. It is false
	// for a Peripheral whose bond is gone from the daemon. The operator
	// reports that gap and never acts on it.
	Held bool `json:"held"`

	Secret   string `json:"secret,omitempty"`
	PairedAt string `json:"pairedAt,omitempty"`
	Request  string `json:"request,omitempty"`
}

// BatteryStatus is the charge the device reports, read from the
// kernel's power supply class where the kernel registers one, and
// from BlueZ's org.bluez.Battery1 interface where it does not.
//
// Source names where the level came from: the power supply's own name,
// such as ps-controller-battery-<address>, for a kernel reading, and
// BlueZ's own word, such as HID or a GATT service, for a BlueZ one.
//
// Charging is absent for a BlueZ reading, because Battery1 reports a
// level and no direction, and for a kernel reading whose status is
// Unknown.
type BatteryStatus struct {
	Percentage int    `json:"percentage"`
	Source     string `json:"source,omitempty"`
	Charging   *bool  `json:"charging,omitempty"`
}

// Condition is the standard Kubernetes condition shape.
//
// The shape is the standard one, so a reader treats these the way it
// treats a Pod's conditions. LastTransitionTime marks when Status last
// changed, not when the operator last wrote the object, so a reader
// measures from it how long a controller has been asleep.
type Condition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	LastTransitionTime string `json:"lastTransitionTime,omitempty"`
}

// PairingRequest is the act of pairing, and it also runs the
// discovery scan. The scan and the pairing window are one radio
// session, so an address a person approves is one the radio observed
// in this same session.
//
// An empty spec.device never pairs anything. Pairing whatever responds
// first is the one behavior that can bond a stranger's device, so the
// address a person writes into the spec is the approval.
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

// window is how long this request's radio session runs.
func (s PairingRequestSpec) window() time.Duration {
	seconds := s.WindowSeconds
	if seconds <= 0 {
		seconds = defaultWindowSeconds
	}
	return time.Duration(seconds) * time.Second
}

// ttl is how long a finished request stays before the operator deletes
// it.
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
	Peripheral     string       `json:"peripheral,omitempty"`

	// FinishedAt is when the request reached Paired or Expired, and the
	// TTL counts from it. Job has the same field for the same reason: a
	// TTL needs a start, and the object's own creationTimestamp is the
	// wrong one because a window can run for minutes.
	FinishedAt string `json:"finishedAt,omitempty"`

	// Message explains a request that did not do what was asked, for
	// example a Pair call bluetoothd refused. It is empty when there is
	// nothing to report.
	Message string `json:"message,omitempty"`
}

// finished reports whether this request has reached an end state. A
// finished request runs no window and holds no radio state.
func (s PairingRequestStatus) finished() bool {
	return s.Phase == phasePaired || s.Phase == phaseExpired
}

// SeenDevice is one device the radio observed during the window. A
// person reads the list to find the address to approve.
type SeenDevice struct {
	Address   string `json:"address"`
	Name      string `json:"name,omitempty"`
	FirstSeen string `json:"firstSeen,omitempty"`
}

// The list types. The API server's list responses include far more
// than this, and the operator reads the items and nothing else.
type (
	AdapterList struct {
		Items []Adapter `json:"items"`
	}
	PeripheralList struct {
		Items []Peripheral `json:"items"`
	}
	PairingRequestList struct {
		Items []PairingRequest `json:"items"`
	}
)

// The paths for these objects. A cluster-scoped resource has one
// collection, and a namespaced resource has one for each namespace and
// one across all of them, which is the one a controller lists.
func adaptersPath() string { return pairingBase + "/adapters" }

func adapterPath(name string) string { return adaptersPath() + "/" + name }

func peripheralsPath() string { return pairingBase + "/peripherals" }

func peripheralPath(name string) string { return peripheralsPath() + "/" + name }

func pairingRequestsPath() string { return pairingBase + "/pairingrequests" }

func pairingRequestPath(namespace, name string) string {
	return pairingBase + "/namespaces/" + namespace + "/pairingrequests/" + name
}

// statusPath names the status subresource of an object. A write there
// changes status and nothing else, so an operator that writes status
// cannot overwrite a spec a person edited in the same moment.
func statusPath(path string) string { return path + "/status" }

// byAdapter narrows a list to the objects that belong to one radio. The
// Peripherals and the bond Secrets both have the adapter's address as a
// label, because a label is selectable and a name is not, and this
// operator must never write another radio's objects.
func byAdapter(path, adapterKey string) string {
	query := url.Values{}
	query.Set("labelSelector", bonds.AdapterLabel+"="+adapterKey)
	return path + "?" + query.Encode()
}

// fromCache makes the API server serve a list from its watch cache
// instead of the datastore. The operator lists PairingRequests on a
// timer, and a list with no resourceVersion reads through to etcd
// every time. The cache can be a moment behind, which can delay the
// first pass on a just-created request by one poll interval.
func fromCache(path string) string {
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	return path + separator + "resourceVersion=0"
}

// createObject posts a new object to its collection and returns what
// the server stored. The stored copy includes the UID that an owner
// reference needs.
func createObject[T any](c *Client, collection string, object *T) (*T, error) {
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

// replaceStatus writes one object's status. The write sends the whole
// object, because that is what the subresource takes, and the server
// keeps only the status half of it.
//
// Every caller states the object's apiVersion and kind before it calls
// this. An object read out of a list does not always have them, and
// the API server refuses a write that includes neither.
func replaceStatus[T any](c *Client, path string, object *T) error {
	body, err := json.Marshal(object)
	if err != nil {
		return err
	}
	return c.RequestJSON(http.MethodPut, statusPath(path), body, nil)
}

// deleteObject removes one object. An object that is already gone
// counts as success, because every caller here needs the object to be
// absent, not a report on one delete call.
func deleteObject(c *Client, path string) error {
	err := c.RequestJSON(http.MethodDelete, path, nil, nil)
	if err == ErrNotFound {
		return nil
	}
	return err
}

// mergePatchType is the content type of a JSON merge patch, which is
// how the operator edits a finalizer list.
//
// A merge patch and not a full replace, because a replace sends every
// field this program models and drops every field it does not, and
// that would drop a person's annotations on an Adapter. The patch
// includes the resourceVersion, which makes the write conditional the
// same way a replace is.
const mergePatchType = "application/merge-patch+json"

// patchFinalizers replaces an object's finalizer list, and returns the
// resourceVersion the write produced.
//
// The caller needs that version. A patch is a write like any other, so
// it produces a new resourceVersion, and the server would refuse a
// second write in the same pass that still stated the version from
// before the patch.
func patchFinalizers(c *Client, path, resourceVersion string, finalizers []string) (string, error) {
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
	if err := c.RequestWithType(http.MethodPatch, path, mergePatchType, body, &patched); err != nil {
		return "", err
	}
	return patched.Metadata.ResourceVersion, nil
}

// timestamp writes a time the way the API server writes one: seconds,
// in UTC, in RFC 3339.
func timestamp(at time.Time) string { return at.UTC().Format(time.RFC3339) }

// parseTimestamp reads a time this operator or the API server wrote. A
// value that does not parse returns the zero time, which every caller
// treats as "no deadline yet", because a status field a person
// hand-edited must not stall a controller.
func parseTimestamp(text string) time.Time {
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return time.Time{}
	}
	return at
}
