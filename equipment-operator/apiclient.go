package main

// The shared apiclient package sends every write, and every read of a
// kind that a reader also writes. The watches run on client-go's
// reflector, in the shared informer package, through a dynamic client
// that this Client builds from the same address and credentials
// (watch.go), and a pass reads the other kinds from the watches' stores
// (objectcache.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/equipment-operator/equipment"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// serviceAccountDir is a variable so a test points it at a directory
// it controls.
var serviceAccountDir = apiclient.ServiceAccountDir

// Client is the shared client, with what the watches and the memo need
// beside it.
type Client struct {
	*apiclient.Client
	*access
}

// access is what every copy of one Client shares (withContext).
type access struct {
	// base and credentials are the address and the credentials
	// directory the shared client reaches the API server with. The
	// watches and the Deployment's leader election build their client-go
	// configuration from the same two (restConfig).
	base        string
	credentials string

	// versions is the memo of each kind this process writes, which each
	// write notes and each read from a store consults (objectcache.go).
	versions objectVersions

	// The dynamic client of the watches, which watcher builds once.
	watchOnce   sync.Once
	watchClient dynamic.Interface
	watchErr    error
}

// NewClient builds a client from its three parts. InClusterClient
// reads them from the pod's environment; a test hands in an
// httptest server's base and no credentials.
func NewClient(base string, httpClient *http.Client, credentials string) *Client {
	return &Client{
		Client: apiclient.New(base, httpClient, credentials),
		access: &access{base: base, credentials: credentials, versions: newObjectVersions()},
	}
}

// InClusterClient builds the client from the pod's environment and its
// ServiceAccount.
func InClusterClient() (*Client, error) {
	shared, err := apiclient.InCluster(apiclient.InClusterOptions{ServiceAccountDir: serviceAccountDir})
	if err != nil {
		return nil, err
	}
	base := "https://" + os.Getenv("KUBERNETES_SERVICE_HOST") + ":" + os.Getenv("KUBERNETES_SERVICE_PORT")
	return &Client{
		Client: shared,
		access: &access{base: base, credentials: serviceAccountDir, versions: newObjectVersions()},
	}, nil
}

// withContext answers the client with requests that end when ctx ends,
// and so does the wait after a 429. A loop that stops then waits on no
// API server. The copy shares the memo and the watches' client.
func (c *Client) withContext(ctx context.Context) *Client {
	return &Client{Client: c.Client.WithContext(ctx), access: c.access}
}

// An apply is a body the API server reads as a partial object under the
// caller's field manager. The apply media type is named for YAML and
// accepts JSON, because YAML is its superset.
const applyContentType = "application/apply-patch+yaml"

// fieldManager names this operator's writes to the API server, so
// server-side apply gives it the fields it applies and leaves every
// other writer's fields alone.
const fieldManager = "equipment-operator"

// A Receiver is cluster-scoped, so the collection path carries no
// namespace, the way a StorageClass path carries none.
const receiversPath = "/apis/" + equipmentAPIVersion + "/receivers"

func receiverPath(name string) string {
	return receiversPath + "/" + name
}

// ListReceivers answers a whole pass with one request.
func ListReceivers(c *Client) (*ReceiverList, error) {
	list := &ReceiverList{}
	if err := c.RequestJSON(http.MethodGet, receiversPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// readReceivers answers every Receiver from the watch's store, and
// lists them from the API server while the store has nothing to give
// (objectcache.go).
func readReceivers(c *Client, held *watchStore) (*ReceiverList, error) {
	if view := held.view(); view.Ready() {
		items, err := informer.CurrentList[Receiver](c.Client, informer.Held{View: view, Versions: c.versions.receivers}, receiverPath)
		return &ReceiverList{Items: items}, err
	}
	return ListReceivers(c)
}

func GetReceiver(c *Client, name string) (*Receiver, error) {
	return apiclient.Get[Receiver](c.Client, receiverPath(name))
}

// receiverStatusApply is the partial object an apply sends: the
// identity the API server matches on, and the status this operator
// owns. It carries no spec, so an apply can never state a field a
// person declared.
type receiverStatusApply struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   ObjectMeta     `json:"metadata"`
	Status     ReceiverStatus `json:"status"`
}

// ApplyReceiverStatus writes the status subresource under this
// operator's own field manager. Server-side apply keeps the write to
// the fields the body states, and removes the fields this manager
// owned and no longer states, so a reading that stops arriving leaves
// no stale value behind. The media operator writes status.session
// under its own field manager, and a ReceiverStatus has no session
// field, so this apply never states the session, never takes it over
// with force, and never removes it. force settles a conflict in this
// manager's favour, because no other writer states the fields a
// ReceiverStatus holds.
func ApplyReceiverStatus(c *Client, name string, status ReceiverStatus) (*Receiver, error) {
	body, err := json.Marshal(&receiverStatusApply{
		APIVersion: equipmentAPIVersion,
		Kind:       "Receiver",
		Metadata:   ObjectMeta{Name: name},
		Status:     status,
	})
	if err != nil {
		return nil, err
	}
	return applyReceiver(c, name, receiverPath(name)+"/status?fieldManager="+fieldManager+"&force=true", body)
}

// receiverPowerApply is the partial object an apply sends to own the
// spec.power field: the identity, and the one field this operator
// manages. It carries no status and no other spec field, so an apply
// can never overwrite what a person or the media operator declared.
type receiverPowerApply struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       ReceiverSpec `json:"spec"`
}

// ApplyReceiverPower writes spec.power on the main resource under this
// operator's own field manager, the way the media operator owns
// status.session. Server-side apply keeps the write to the field the body
// states, so a GitOps manifest that does not declare power never
// touches the value this operator settled on. force settles a conflict
// in this manager's favour, because nothing else writes a Receiver's
// power.
func ApplyReceiverPower(c *Client, name string, power equipment.Power) (*Receiver, error) {
	body, err := json.Marshal(&receiverPowerApply{
		APIVersion: equipmentAPIVersion,
		Kind:       "Receiver",
		Metadata:   ObjectMeta{Name: name},
		Spec:       ReceiverSpec{Power: power},
	})
	if err != nil {
		return nil, err
	}
	return applyReceiver(c, name, receiverPath(name)+"?fieldManager="+fieldManager+"&force=true", body)
}

// applyReceiver sends one apply of a Receiver, and answers the copy the
// API server stored, whose version the memo notes.
func applyReceiver(c *Client, name, path string, body []byte) (*Receiver, error) {
	return memo.Written[Receiver](c.versions.receivers, name, func() (*Receiver, error) {
		answer := &Receiver{}
		return answer, c.Request(http.MethodPatch, path, applyContentType, body, answer)
	})
}

// discoveredLabel marks a Receiver the discovery loop created for an
// amp it found, and holds the protocol it was found by. The label is
// what lets the operator prune its own objects and never a person's:
// only an object carrying it is one the operator made.
const discoveredLabel = "equipment.liken.sh/discovered"

// discoveredReceiverApply is the partial object discovery applies: the
// identity, the marker label, and the protocol block. It carries no
// inputs, no session, and no settings, so every field a person later
// writes stays theirs.
type discoveredReceiverApply struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       ReceiverSpec `json:"spec"`
}

// ApplyDiscoveredReceiver creates, or keeps, the Receiver the discovery
// loop owns for one amp. It applies under this operator's own field
// manager, so a person's later writes to the inputs, the session, or
// the settings are untouched, and the identity block it does state is
// the one field it owns.
func ApplyDiscoveredReceiver(c *Client, name, uuid string) (*Receiver, error) {
	body, err := json.Marshal(&discoveredReceiverApply{
		APIVersion: equipmentAPIVersion,
		Kind:       "Receiver",
		Metadata: ObjectMeta{
			Name:   name,
			Labels: map[string]string{discoveredLabel: "wiim"},
		},
		Spec: ReceiverSpec{Wiim: &WiimProtocol{UUID: uuid}},
	})
	if err != nil {
		return nil, err
	}
	return applyReceiver(c, name, receiverPath(name)+"?fieldManager="+fieldManager+"&force=true", body)
}

// DeleteReceiver removes one Receiver. A Receiver the operator did not
// create is never named here: discovery deletes only the objects that
// carry its own marker label. A name that is already gone is not an
// error, so a prune that races a deletion settles the same way.
func DeleteReceiver(c *Client, name string) error {
	return c.versions.receivers.Send(name, func() (string, error) {
		return "", deleteObject(c, receiverPath(name), "receiver "+name)
	})
}

// deleteObject removes one object. A name that is already gone is not
// an error. what names the object in the error.
func deleteObject(c *Client, path, what string) error {
	err := c.RequestJSON(http.MethodDelete, path, nil, nil)
	if err == nil || err == apiclient.ErrNotFound {
		return nil
	}
	return fmt.Errorf("deleting %s: %w", what, err)
}

// settingsPath maps a bus setting id onto the leaf of
// spec.denon.settings that holds it. The channel family lives under
// channelVolumes, and every other id maps by its dotted segments, so
// tone.bass becomes ["tone","bass"] and channel.FL becomes
// ["channelVolumes","FL"].
func settingsPath(id string) []string {
	if strings.HasPrefix(id, "channel.") {
		return []string{"channelVolumes", strings.TrimPrefix(id, "channel.")}
	}
	return strings.Split(id, ".")
}

// settingsLeafSpec is the body of an apply that owns one leaf of one
// protocol's settings: the protocol block, and the one nested field
// this operator manages. It is built as raw JSON because the leaf path
// is dynamic and no typed struct can name it. The protocol key keeps a
// Denon write and a WiiM write in their own spec blocks.
func settingsLeafSpec(protocol string, leaf []string, value equipment.SettingValue) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var node any = json.RawMessage(encoded)
	for index := len(leaf) - 1; index >= 0; index-- {
		node = map[string]any{leaf[index]: node}
	}
	return json.Marshal(map[string]any{protocol: map[string]any{"settings": node}})
}

// applySettingsLeaf writes one leaf of one protocol's settings on the
// main resource under this operator's own field manager, the way
// ApplyReceiverPower owns spec.power. Server-side apply keeps the write
// to the one leaf the body states, so a bus write to one key never
// claims the keys around it and never touches a key a GitOps manifest
// declared. That is the one-writer invariant: a manifest-declared key
// is Flux's, and a bus-written key is this operator's, and the two
// never own the same leaf. force settles a conflict in this manager's
// favour, because nothing else writes a bus-written key.
func applySettingsLeaf(c *Client, name, protocol string, leaf []string, value equipment.SettingValue) (*Receiver, error) {
	spec, err := settingsLeafSpec(protocol, leaf, value)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(struct {
		APIVersion string          `json:"apiVersion"`
		Kind       string          `json:"kind"`
		Metadata   ObjectMeta      `json:"metadata"`
		Spec       json.RawMessage `json:"spec"`
	}{
		APIVersion: equipmentAPIVersion,
		Kind:       "Receiver",
		Metadata:   ObjectMeta{Name: name},
		Spec:       spec,
	})
	if err != nil {
		return nil, err
	}
	return applyReceiver(c, name, receiverPath(name)+"?fieldManager="+fieldManager+"&force=true", body)
}

// ApplyReceiverSettings writes one leaf of spec.denon.settings.
func ApplyReceiverSettings(c *Client, name string, leaf []string, value equipment.SettingValue) (*Receiver, error) {
	return applySettingsLeaf(c, name, "denon", leaf, value)
}

// ApplyReceiverWiimSettings writes one leaf of spec.wiim.settings. It
// is the same one-writer path in WiiM's own block, so a bus write to a
// WiiM key and one to a Denon key never touch the same spec.
func ApplyReceiverWiimSettings(c *Client, name string, leaf []string, value equipment.SettingValue) (*Receiver, error) {
	return applySettingsLeaf(c, name, "wiim", leaf, value)
}
