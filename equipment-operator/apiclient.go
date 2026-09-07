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
	"time"
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
