package main

// This is a Kubernetes client written straight against the HTTP API,
// following liken's own (kubernetes/apiclient.go) and the media
// operator's, for the same reason: the API is HTTPS that serves
// JSON, and each read and write is one request. It sends every write,
// and every read of a kind that the reader also writes. The watches run
// on client-go's reflector, in watch.go, through a dynamic client that
// this Client builds from the same address and credentials, and a pass
// reads the other kinds from the watches' stores (watchcache.go).
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
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"k8s.io/client-go/dynamic"

	"github.com/liken-sh/equipment-operator/equipment"
)

// apiRequestTimeout bounds one request of the Client from the dial to
// the last byte of the body. Every request the Client sends is one
// read or one write, and none streams: the watches run on client-go's
// own client (watch.go). A pass that waits on a request waits at most
// this long, and the backstop or the next event tries it again. It is
// a variable so a test holds it short.
var apiRequestTimeout = 30 * time.Second

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
	return &Client{base: base, http: httpClient, credentials: credentials, versions: newObjectVersions()}
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
		Timeout: apiRequestTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots},
			// Each timeout bounds the same failure: a server that
			// stops answering without sending anything.
			// apiRequestTimeout bounds the whole request as well, so
			// a body that stops part way cannot hold a pass.
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
		message := responseText(resp.Body)
		err := fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, message)
		if resp.StatusCode == http.StatusTooManyRequests {
			return &throttledError{err: err, wait: retryAfter(resp.Header.Get("Retry-After"), []byte(message))}
		}
		return err
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// retryAfterUnit is one second of an API server's retry advice. It is
// a variable so a test waits milliseconds instead.
var retryAfterUnit = time.Second

// throttledError is a 429 from the API server, which asks the client
// to wait and ask again. The server answers so for a second or two
// while the storage of a CRD it just received starts, with the reason
// "storage is (re)initializing".
type throttledError struct {
	err  error
	wait time.Duration
}

func (e *throttledError) Error() string { return e.err.Error() }
func (e *throttledError) Unwrap() error { return e.err }

// retryAfter reads how long a 429 asks the client to wait: the
// Retry-After header, or the retryAfterSeconds of the Status body, or
// one second when the answer states neither.
func retryAfter(header string, body []byte) time.Duration {
	if seconds, err := strconv.Atoi(header); err == nil && seconds > 0 {
		return time.Duration(seconds) * retryAfterUnit
	}
	var status struct {
		Details struct {
			RetryAfterSeconds int `json:"retryAfterSeconds"`
		} `json:"details"`
	}
	if json.Unmarshal(body, &status) == nil && status.Details.RetryAfterSeconds > 0 {
		return time.Duration(status.Details.RetryAfterSeconds) * retryAfterUnit
	}
	return retryAfterUnit
}

// retryThrottled makes a call until it answers something other than a
// 429, waiting as long as each 429 asks. It stops when ctx ends and
// answers the last 429 then. Any other answer, an error included, is
// the caller's.
func retryThrottled(ctx context.Context, call func() error) error {
	for {
		err := call()
		var throttled *throttledError
		if !errors.As(err, &throttled) {
			return err
		}
		fmt.Fprintf(os.Stderr, "%v; asking again in %s\n", err, throttled.wait)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(throttled.wait):
		}
	}
}

// responseText is the start of an answer's body, for an error that
// carries the server's own text. The API server ends its Status body
// with a newline, and the text leaves it out, so the error and the log
// line that prints it stay on one line.
func responseText(body io.Reader) string {
	message, _ := io.ReadAll(io.LimitReader(body, 2048))
	return strings.TrimRightFunc(string(message), unicode.IsSpace)
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
	if view := held.view(); view.ready() {
		items, err := currentList[Receiver](c, heldObjects{view: view, versions: c.versions.receivers}, receiverPath)
		return &ReceiverList{Items: items}, err
	}
	return ListReceivers(c)
}

func GetReceiver(c *Client, name string) (*Receiver, error) {
	return get[Receiver](c, receiverPath(name))
}

// get reads one object.
func get[T any](c *Client, path string) (*T, error) {
	out := new(T)
	if err := c.RequestJSON(http.MethodGet, path, nil, out); err != nil {
		return nil, err
	}
	return out, nil
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
	return written[Receiver](c.versions.receivers, name, func() (*Receiver, error) {
		answer := &Receiver{}
		return answer, c.requestJSON(http.MethodPatch, path, applyContentType, body, answer)
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
	return c.versions.receivers.send(name, func() (string, error) {
		return "", deleteObject(c, receiverPath(name), "receiver "+name)
	})
}

// deleteObject removes one object. A name that is already gone is not
// an error. what names the object in the error.
func deleteObject(c *Client, path, what string) error {
	resp, err := c.send(context.Background(), http.MethodDelete, path, "", nil)
	if err != nil {
		return err
	}
	defer drain(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message := responseText(resp.Body)
		return fmt.Errorf("deleting %s: %s: %s", what, resp.Status, message)
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
