package main

// The operator's API client, and the API calls for Receiver. Writes and
// direct reads go through the shared apiclient package. The watches use
// client-go's reflector, through a dynamic client built from the same
// address and credentials (watch.go).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/liken/equipment-operator/equipment"
	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
	"github.com/liken-sh/liken/kubernetes/memo"
)

// serviceAccountDir is a variable so a test can point it at its own
// directory.
var serviceAccountDir = apiclient.ServiceAccountDir

// Client is the shared apiclient.Client plus the state of the watches
// and of the version memo.
type Client struct {
	*apiclient.Client
	*access
}

// access is the state that every copy of one Client shares, including
// the copies that withContext and withWaits make.
type access struct {
	// The watches and the Deployment's leader election build their
	// client-go configuration from these (restConfig).
	base        string
	credentials string

	// transport is the HTTP client's round tripper from NewClient, or nil
	// for the default. The watches send through it, so in a test they
	// reach the same fake API server as every other request.
	transport http.RoundTripper

	// dial opens the watches' connections, or nil for client-go's own
	// dialer. A test that serves the API server over TLS on an
	// in-memory network passes that network's dial, because client-go
	// refuses a custom Transport together with a CA file.
	dial dialFunc

	// versions records the resourceVersion of each object this process
	// wrote, so a read does not use a store's copy that is older than
	// this process's last write (objectcache.go).
	versions objectVersions

	// The dynamic client that the watches share. watcher builds it on
	// first use.
	watchOnce   sync.Once
	watchClient dynamic.Interface
	watchErr    error
}

// NewClient builds a client for the API server at base. A test passes
// its fake API server's client and no credentials directory.
func NewClient(base string, httpClient *http.Client, credentials string) *Client {
	return &Client{
		Client: apiclient.New(base, httpClient, credentials),
		access: &access{base: base, credentials: credentials, transport: httpClient.Transport, versions: newObjectVersions()},
	}
}

// InClusterClient builds the client from the pod's environment and
// ServiceAccount. timeout bounds each request, and zero uses the shared
// client's default.
func InClusterClient(timeout time.Duration) (*Client, error) {
	shared, err := apiclient.InCluster(apiclient.InClusterOptions{ServiceAccountDir: serviceAccountDir, Timeout: timeout})
	if err != nil {
		return nil, err
	}
	base := "https://" + os.Getenv("KUBERNETES_SERVICE_HOST") + ":" + os.Getenv("KUBERNETES_SERVICE_PORT")
	return &Client{
		Client: shared,
		access: &access{base: base, credentials: serviceAccountDir, versions: newObjectVersions()},
	}, nil
}

// withContext returns a copy of the client whose requests, and whose
// waits after a 429, are canceled when ctx ends.
func (c *Client) withContext(ctx context.Context) *Client {
	return &Client{Client: c.Client.WithContext(ctx), access: c.access}
}

// withWaits returns a copy of the client that stops waiting after a 429
// when ctx ends, but lets a request already sent run to completion. The
// Deployment releases its Lease after its last write. A canceled write
// can still reach the API server, possibly after another replica took
// the Lease, so the loops that write cancel only their waits.
func (c *Client) withWaits(ctx context.Context) *Client {
	return &Client{Client: c.Client.WithWaitContext(ctx), access: c.access}
}

const startRetry = time.Second

// retryWhileThrottled retries call after each 429 until it returns anything
// else or ctx ends. The API server returns 429 while it sets up the
// storage for a new CRD, and a process that exited on it would restart
// into the same 429. Any other error, such as a missing RBAC grant,
// returns at once. The client has already retried each 429 up to its own
// limit, so a 429 that reaches this loop asked for a longer wait. The
// loop waits as long as Retry-After asks, and at least startRetry.
func retryWhileThrottled(ctx context.Context, call func() error) error {
	for {
		err := call()
		if !errors.Is(err, apiclient.ErrThrottled) {
			return err
		}
		wait := max(startRetry, time.Duration(apiclient.RetryAfterSeconds(err))*time.Second)
		fmt.Fprintf(os.Stderr, "%v, retrying in %s\n", err, wait)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
}

// applyContentType marks a PATCH as a server-side apply. The media type
// names YAML, and JSON is valid YAML, so the bodies here are JSON.
const applyContentType = "application/apply-patch+yaml"

// fieldManager is the default field manager of this operator. The API
// server records which fields each manager set, and an apply removes the
// fields that its manager set before and the new body leaves out.
const fieldManager = "equipment-operator"

// A Receiver is cluster-scoped, so its collection path has no
// namespace.
const receiversPath = "/apis/" + equipmentAPIVersion + "/receivers"

func receiverPath(name string) string {
	return receiversPath + "/" + name
}

// ListReceivers lists every Receiver from the API server, not from the
// watch's store.
func ListReceivers(c *Client) (*ReceiverList, error) {
	list := &ReceiverList{}
	if err := c.RequestJSON(http.MethodGet, receiversPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// readReceivers returns every Receiver from the watch's store, or from
// a list on the API server until the store is ready (objectcache.go).
func readReceivers(c *Client, store *watchStore) (*ReceiverList, error) {
	items, err := informer.List[Receiver](c.Client, informer.Held{View: store.view(), Versions: c.versions.receivers}, receiversPath, receiverPath)
	return &ReceiverList{Items: items}, err
}

// GetReceiver reads one Receiver from the API server. A Receiver that
// does not exist returns apiclient.ErrNotFound.
func GetReceiver(c *Client, name string) (*Receiver, error) {
	return apiclient.Get[Receiver](c.Client, receiverPath(name))
}

type receiverStatusApply struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Metadata   ObjectMeta     `json:"metadata"`
	Status     ReceiverStatus `json:"status"`
}

// ApplyReceiverStatus applies status to the Receiver's status
// subresource. A field that status leaves empty is removed, so a reading
// that stops arriving does not leave a stale value. media-operator
// writes status.session under its own field manager, and ReceiverStatus
// has no session field, so this apply leaves the session alone.
// force=true is safe because no other writer sets a field of
// ReceiverStatus.
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

type receiverPowerApply struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       ReceiverSpec `json:"spec"`
}

// ApplyReceiverPower applies spec.power, and no other field, to the
// Receiver itself. A GitOps manifest that does not declare power then
// leaves this operator's value in place. force=true is safe because no
// other writer sets a Receiver's power.
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

// applyReceiver sends one apply and records the resourceVersion of the
// returned copy in the memo.
func applyReceiver(c *Client, name, path string, body []byte) (*Receiver, error) {
	return memo.Written[Receiver](c.versions.receivers, name, func() (*Receiver, error) {
		answer := &Receiver{}
		return answer, c.Request(http.MethodPatch, path, applyContentType, body, answer)
	})
}

// discoveredLabel marks an object that the operator created for a
// device it discovered, and its value says how the device was found.
// Pruning deletes only objects with this label, so it never deletes an
// object that a person created.
const discoveredLabel = "equipment.liken.sh/discovered"

type discoveredReceiverApply struct {
	APIVersion string       `json:"apiVersion"`
	Kind       string       `json:"kind"`
	Metadata   ObjectMeta   `json:"metadata"`
	Spec       ReceiverSpec `json:"spec"`
}

// ApplyDiscoveredReceiver creates the Receiver for a discovered WiiM
// amp, or sets its spec.wiim.uuid when the Receiver exists. The apply
// sets only the label and spec.wiim, so it leaves alone the inputs,
// session, and settings that a person adds later.
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

// DeleteReceiver deletes one Receiver. A Receiver that is already gone
// is not an error, so a prune that races another deletion succeeds.
func DeleteReceiver(c *Client, name string) error {
	return c.versions.receivers.Send(name, func() (string, error) {
		return "", deleteObject(c, receiverPath(name), "Receiver "+name)
	})
}

// deleteObject deletes the object at path, and treats a 404 as success.
// what describes the object in the error message.
func deleteObject(c *Client, path, what string) error {
	err := c.RequestJSON(http.MethodDelete, path, nil, nil)
	if err == nil || err == apiclient.ErrNotFound {
		return nil
	}
	return fmt.Errorf("deleting %s: %w", what, err)
}
