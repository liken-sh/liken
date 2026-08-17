package main

// A Kubernetes API client, built from first principles.
//
// This follows liken's own client (kubernetes/apiclient.go) and for
// the same reason: the Kubernetes API is HTTPS that serves JSON, and
// this program speaks to three URLs. client-go would bring informers,
// work queues, and generated types this program does not use, into a
// container that also carries BlueZ.
//
// Every pod starts with what it needs to reach the API server.
// Kubernetes injects two environment variables that name the server's
// in-cluster address, and the kubelet mounts a CA certificate and a
// ServiceAccount token at a known path. Those five values are the
// whole of what client-go's rest.InClusterConfig() reads.

import (
	"bytes"
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

// serviceAccountDir names the path where the kubelet mounts each
// container's API credentials. It is a variable so tests can point it
// at a directory they control.
var serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// ErrNotFound marks the difference between "this object does not
// exist" and a real failure. An absent object is a normal state, and
// the caller handles it by creating the object. ErrConflict marks the
// same difference for "something else wrote this object first", which
// is normal under optimistic concurrency, and the caller handles it
// by reading the object again.
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
// gets these parts from the pod's environment, and tests get them
// from an httptest server.
func NewClient(base string, httpClient *http.Client, credentials string) *Client {
	return &Client{base: base, http: httpClient, credentials: credentials}
}

func InClusterClient() (*Client, error) {
	host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, fmt.Errorf("not running in a cluster: KUBERNETES_SERVICE_HOST unset")
	}

	// The mounted CA is the cluster's own. The client trusts only that
	// CA, not the system trust store, so it accepts the cluster's API
	// server and rejects any other server that answers on the address.
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
			// Each timeout limits the same failure: a server that stops
			// answering without sending anything. A machine that fails
			// sends no FIN and no RST, so a connection to it goes silent
			// and every wait on it would otherwise have no limit.
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 10 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		},
		Timeout: 30 * time.Second,
	}, serviceAccountDir), nil
}

// RequestJSON sends one request and decodes the JSON response into
// out. It turns any non-2xx status into an error that carries the
// server's own message.
func (c *Client) RequestJSON(method, path string, body []byte, out any) error {
	return c.RequestWithType(method, path, "application/json", body, out)
}

// RequestWithType is RequestJSON with the request's own content type
// stated. A PATCH needs it: the API server reads the patch's dialect
// from the header alone, and the same bytes mean different things as a
// merge patch and as a JSON patch.
func (c *Client) RequestWithType(method, path, contentType string, body []byte, out any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return err
	}
	// The in-cluster client reads its token from disk on every
	// request. The tokens are short-lived and the kubelet refreshes
	// the mounted file as each one nears its expiry, so a client that
	// holds a token in memory eventually gets 401 responses.
	if c.credentials != "" {
		token, err := os.ReadFile(c.credentials + "/token")
		if err != nil {
			return fmt.Errorf("reading service account token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+string(token))
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
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
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// get sends a GET request for a single object.
func get[T any](c *Client, path string) (*T, error) {
	out := new(T)
	if err := c.RequestJSON(http.MethodGet, path, nil, out); err != nil {
		return nil, err
	}
	return out, nil
}

// maxDrain bounds the read below. The largest answer this program
// asks for is one ResourceSlice, which the caller decodes into memory
// anyway, so reading the tail costs nothing new.
const maxDrain = 4 << 20

// drain reads whatever the caller left in the response body, then
// closes it. Go hands a connection back to its pool only when the
// body reaches EOF, so a body closed early costs a fresh TCP
// connection and TLS handshake on the next request, and reaches the
// API server as a hang-up on a request it already answered.
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}
