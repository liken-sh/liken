package main

// This is a Kubernetes client written straight against the HTTP API,
// following liken's own (kubernetes/apiclient.go) and the media
// operator's, for the same reason: the API is HTTPS that serves
// JSON, and client-go would bring informers, work queues, and
// generated types this program does not use.
//
// Every pod already holds what it needs to reach the API server.
// Kubernetes injects two environment variables that name the
// server's in-cluster address, and the kubelet mounts a CA
// certificate and a ServiceAccount token at a known path. Those five
// values are the whole of an in-cluster config.

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/liken-sh/equipment-operator/equipment"
)

// serviceAccountDir is a variable so a test points it at a directory
// it controls.
var serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// These two answers are values, not failures. An absent object is
// the normal state the caller answers by creating it, and a conflict
// is the normal state under optimistic concurrency that the caller
// answers by reading again.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict: something else wrote this object first")
)

type Client struct {
	base        string
	http        *http.Client
	credentials string
}

// NewClient builds a client from its three parts. InClusterClient
// reads them from the pod's environment; a test hands in an
// httptest server's base and no credentials.
func NewClient(base string, httpClient *http.Client, credentials string) *Client {
	return &Client{base: base, http: httpClient, credentials: credentials}
}

func InClusterClient() (*Client, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in a cluster: KUBERNETES_SERVICE_HOST unset")
	}

	// The client trusts the cluster's own CA and not the system
	// store, so it accepts this API server and no other server that
	// answers on the address.
	caPEM, err := os.ReadFile(serviceAccountDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("reading service account CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("service account CA contains no certificates")
	}

	return NewClient("https://"+host+":"+port, &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots},
			// Each timeout bounds the same failure: a server that
			// stops answering without sending anything. There is no
			// overall client timeout, because the watch is a request
			// whose response never ends, and a whole-request
			// deadline would cut the stream on schedule.
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 10 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		},
	}, serviceAccountDir), nil
}

// The two content types this client sends. A body is JSON,
// except an apply, which the API server reads as a partial object
// under the caller's field manager. The apply media type is named for
// YAML and accepts JSON, because YAML is its superset.
const (
	jsonContentType  = "application/json"
	applyContentType = "application/apply-patch+yaml"
)

// fieldManager names this operator's writes to the API server, so
// server-side apply gives it the fields it applies and leaves every
// other writer's fields alone.
const fieldManager = "equipment-operator"

// Do sends one request and hands back the open response, which is
// what the watch needs and what RequestJSON is built on. The context
// governs the whole exchange, the response body included, so
// cancelling it is what ends a read of a stream that never ends.
func (c *Client) Do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	return c.send(ctx, method, path, jsonContentType, body)
}

func (c *Client) send(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, err
	}
	// The token is read from disk on every request. The mounted
	// token is short-lived and the kubelet refreshes the file as
	// each one nears expiry, so a client that held one in memory
	// would start getting 401s.
	if c.credentials != "" {
		token, err := os.ReadFile(c.credentials + "/token")
		if err != nil {
			return nil, fmt.Errorf("reading service account token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+string(token))
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}
	return c.http.Do(req)
}

// RequestJSON sends one request and decodes the answer, turning
// every non-2xx status into an error that carries the server's own
// message.
func (c *Client) RequestJSON(method, path string, body []byte, out any) error {
	return c.requestJSON(method, path, jsonContentType, body, out)
}

func (c *Client) requestJSON(method, path, contentType string, body []byte, out any) error {
	resp, err := c.send(context.Background(), method, path, contentType, body)
	if err != nil {
		return err
	}
	defer drain(resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode == http.StatusConflict {
		return ErrConflict
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, message)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// drain reads whatever the caller left in the body, then closes it.
// Go returns a connection to its pool only when the body reaches
// EOF, so an early close costs a fresh connection and TLS handshake,
// and reaches the server as a hang-up on a request it answered.
const maxDrain = 4 << 20

func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}

// A Receiver is cluster-scoped, so the collection path carries no
// namespace, the way a StorageClass path carries none.
const receiversPath = "/apis/" + equipmentAPIVersion + "/receivers"

func receiverPath(name string) string {
	return receiversPath + "/" + name
}

// ListReceivers answers a whole pass with one request, and the list's
// resourceVersion is where a watch resumes from.
func ListReceivers(c *Client) (*ReceiverList, error) {
	list := &ReceiverList{}
	if err := c.RequestJSON(http.MethodGet, receiversPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

func GetReceiver(c *Client, name string) (*Receiver, error) {
	receiver := &Receiver{}
	if err := c.RequestJSON(http.MethodGet, receiverPath(name), nil, receiver); err != nil {
		return nil, err
	}
	return receiver, nil
}

// WatchReceivers opens the stream the loop wakes on. A watch is an
// ordinary GET whose response never ends: the API server holds the
// connection open and writes one JSON event per change. The caller
// owns the body, and resumes from the resourceVersion a list gave it,
// so no change is missed between reconnects. Bookmarks cost one line
// each and keep the resume point current while nothing changes.
func WatchReceivers(ctx context.Context, c *Client, resourceVersion string) (*http.Response, error) {
	path := receiversPath + "?watch=true&allowWatchBookmarks=true&resourceVersion=" + resourceVersion
	return c.Do(ctx, http.MethodGet, path, nil)
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
// no stale value behind. force settles a conflict in this manager's
// favour, because nothing else writes a Receiver's status.
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
	path := receiverPath(name) + "/status?fieldManager=" + fieldManager + "&force=true"
	written := &Receiver{}
	if err := c.requestJSON(http.MethodPatch, path, applyContentType, body, written); err != nil {
		return nil, err
	}
	return written, nil
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
// spec.session. Server-side apply keeps the write to the field the body
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
	path := receiverPath(name) + "?fieldManager=" + fieldManager + "&force=true"
	written := &Receiver{}
	if err := c.requestJSON(http.MethodPatch, path, applyContentType, body, written); err != nil {
		return nil, err
	}
	return written, nil
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
	path := receiverPath(name) + "?fieldManager=" + fieldManager + "&force=true"
	written := &Receiver{}
	if err := c.requestJSON(http.MethodPatch, path, applyContentType, body, written); err != nil {
		return nil, err
	}
	return written, nil
}

// DeleteReceiver removes one Receiver. A Receiver the operator did not
// create is never named here: discovery deletes only the objects that
// carry its own marker label. A name that is already gone is not an
// error, so a prune that races a deletion settles the same way.
func DeleteReceiver(c *Client, name string) error {
	resp, err := c.send(context.Background(), http.MethodDelete, receiverPath(name), "", nil)
	if err != nil {
		return err
	}
	defer drain(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("deleting receiver %s: %s: %s", name, resp.Status, message)
	}
	return nil
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
	url := receiverPath(name) + "?fieldManager=" + fieldManager + "&force=true"
	written := &Receiver{}
	if err := c.requestJSON(http.MethodPatch, url, applyContentType, body, written); err != nil {
		return nil, err
	}
	return written, nil
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
