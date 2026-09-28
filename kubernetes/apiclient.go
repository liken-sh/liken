// Package kubernetes lets liken's controllers communicate with the
// Kubernetes API. It provides a client built from first principles,
// access to liken's own resources, the heartbeat-lease protocol, and
// pod eviction. The watches are in the informer package below this
// one.
//
// Two programs use this package. The machine operator is a
// privileged DaemonSet that manages the machine it runs on. The
// cluster operator is an unprivileged Deployment that watches the
// fleet.
//
// All code in this package communicates with the API using only
// net/http and encoding/json. It does not use client-go,
// controller-runtime, or code generation. Those libraries hide a
// fact that this package shows: the Kubernetes API is only HTTPS that
// serves JSON, and anything kubectl can do, curl can also do. The one
// part of client-go that liken uses is its reflector, which keeps a
// watch open and recovers it when the stream drops (the informer
// package says why).
package kubernetes

// A Kubernetes API client, built from first principles.
//
// Every pod starts with everything it needs to reach the API server.
// Kubernetes injects two environment variables that name the
// server's in-cluster address. Kubelet mounts a directory of
// credentials into every container at a known path: a CA certificate
// to verify the server, and a ServiceAccount token to authenticate
// the pod. "In-cluster config", the function that client-go's
// rest.InClusterConfig() runs, only reads those five values.
//
// From there, the API is plain REST. Every object lives at a
// predictable URL (/apis/<group>/<version>/<plural>/<name>). A GET
// request reads it. A POST request creates it. A PUT request
// replaces it. Authentication uses one bearer-token header. The
// command kubectl -v=9 prints these exact requests; this file sends
// the same requests directly.

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/liken-sh/liken/api"
)

// serviceAccountDir names the path where kubelet mounts each
// container's API credentials. It is a variable so tests can point
// it at a directory they control, the same seam init's disk code
// leaves open with sysBlock and devRoot.
var serviceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// MachinesPath and ClustersPath name the URLs where our CRDs' objects
// live. Every resource in Kubernetes uses the same URL structure.
// Built-in resources use the legacy /api/v1 root instead of
// /apis/<group>. Both Machines and Clusters are cluster-scoped kinds,
// so their URLs have no /namespaces/<ns>/ segment.
const (
	MachinesPath = "/apis/" + api.APIVersion + "/machines"
	ClustersPath = "/apis/" + api.APIVersion + "/clusters"
)

type Client struct {
	base string
	http *http.Client

	// credentials is the directory that holds the ServiceAccount
	// token. It is a parameter, not the constant above, so tests can
	// point the client at a directory they control.
	credentials string

	// writeGuard, when it is set, runs before every request that is
	// not a GET, and an error from it refuses the request before it is
	// sent. GuardWrites says why.
	writeGuard func() error
}

// GuardWrites makes every write this client sends ask guard first. The
// cluster operator uses it to stop writing the moment its leader
// election Lease is overdue for a renewal (cluster-operator/leader.go).
// A check in one place covers every write a pass makes: the status
// writes, the grants, the evictions, and the deletes.
func (c *Client) GuardWrites(guard func() error) {
	c.writeGuard = guard
}

// NewClient builds a client from its three parts directly.
// InClusterClient gets these parts from the pod's environment. Tests
// get these parts from an httptest server.
func NewClient(base string, httpClient *http.Client, credentials string) *Client {
	return &Client{base: base, http: httpClient, credentials: credentials}
}

func InClusterClient() (*Client, error) {
	return InClusterClientAt("")
}

// InClusterClientAt works like InClusterClient, but it connects to a
// chosen endpoint instead of the injected one. The environment names
// the API service's virtual IP. That virtual IP uses iptables NAT:
// iptables pins every new connection to one API server, and a client
// cannot choose which server it uses. Ordinary pods accept this
// limit. But a hostNetwork pod running on a machine that also runs
// an API server (or k3s's health-checked local load balancer over
// all API servers) has a better address to use: its own loopback
// address, where a dead remote server can never strand a connection.
// The credentials stay the same in both cases. An empty string ("")
// for base means the client uses the environment's endpoint.
func InClusterClientAt(base string) (*Client, error) {
	if base == "" {
		host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
		if host == "" || port == "" {
			return nil, fmt.Errorf("not running in a cluster: KUBERNETES_SERVICE_HOST unset")
		}
		base = "https://" + host + ":" + port
	}

	// The mounted CA is the cluster's server CA. It is the same root
	// certificate that the identity package minted offline before
	// this machine ever booted. The client trusts only that CA, not
	// the system trust store. This means the client accepts only the
	// cluster's own API server, and rejects any other server that
	// answers on that address.
	caPEM, err := os.ReadFile(serviceAccountDir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("reading service account CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("service account CA contains no certificates")
	}

	return NewClient(base, &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots},
			// Every timeout below limits the same failure: a server
			// that stops responding without sending any signal. A
			// machine that fails sends no FIN and no RST packet, so a
			// connection to it just goes silent. Without deadlines,
			// each kind of wait would have no limit. The timeout
			// values are set so that even an unlucky pass that hits
			// several dead connections in a row finishes well inside
			// the forty-second heartbeat window (see heartbeat.go): a
			// client that stalls on another machine's failure must
			// never make this machine appear dead.
			//
			// The dial timeout limits the time spent connecting to an
			// endpoint that no longer answers (by default, a SYN
			// packet sent to a dead address retransmits into silence
			// for minutes). The keep-alive setting makes the kernel
			// probe connections that are established but idle, so a
			// pooled connection to a server that died is found and
			// closed before a request is written onto it.
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,
			// The response headers get a deadline of ten seconds.
			// This is generous for a healthy server, and short enough
			// that a request written onto a silently dead connection
			// fails while the heartbeat still has plenty of time left.
			ResponseHeaderTimeout: 10 * time.Second,
			// A pooled connection that has sat idle is the connection
			// most likely to be silently dead. The server may have
			// closed it, or the network path may have reset it
			// without notice. Go writes the next request onto that
			// connection anyway, and detects the failure only when no
			// answer comes back: the server then receives an orphaned
			// request from a client that already gave up, and logs it
			// as an aborted request. Discarding idle connections
			// after a short wait means the next request opens a
			// fresh connection instead of risking a stale one. An
			// operator's steady request rate keeps its one working
			// connection busier than this timeout, so the timeout
			// only closes the extra connections that bursts of
			// activity open.
			IdleConnTimeout: 30 * time.Second,
		},
		// The whole request, body included, gets a deadline of
		// fifteen seconds. This client sends no watch, because the
		// informer package's client runs every watch, so no request
		// here is meant to stay open. The bound matters most to the
		// cluster operator: a write that its leader election allowed
		// is abandoned by this client before a new leader can take
		// the Lease (cluster-operator/leader.go gives the numbers).
		Timeout: requestTimeout,
	}, serviceAccountDir), nil
}

// requestTimeout bounds one request of an in-cluster client, from the
// dial to the last byte of the answer.
const requestTimeout = 15 * time.Second

// Do sends one request, authenticated when the client has a
// credentials directory.
func (c *Client) Do(method, path, contentType string, body []byte) (*http.Response, error) {
	if c.writeGuard != nil && method != http.MethodGet {
		if err := c.writeGuard(); err != nil {
			return nil, fmt.Errorf("%s %s not sent: %w", method, path, err)
		}
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
	if err != nil {
		return nil, err
	}
	// A workstation client (kubeconfig.go) authenticates in the TLS
	// handshake with its client certificate, so it has no
	// credentials directory and sends no Authorization header. The
	// in-cluster client reads its token from disk every time: the
	// tokens are short-lived, and kubelet refreshes the mounted file
	// as each one nears its expiry, so a client that stores a token
	// in memory eventually gets 401 responses.
	if c.credentials != "" {
		token, err := os.ReadFile(c.credentials + "/token")
		if err != nil {
			return nil, fmt.Errorf("reading service account token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+string(token))
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return c.http.Do(req)
}

// RetryPause sleeps for about five seconds, increased at random by up
// to half again. The random increase matters because a fleet reboots
// together: every machine's operator meets the same not-yet-served
// CRDs and dropped watches at the same moments, and identical retry
// delays would keep every operator retrying at the same moments.
// Randomizing the delay spreads that load over time. RetryPause is a
// variable so tests can replace it with a function that does
// nothing, the same seam init's disk code leaves open with sysBlock
// and devRoot.
var RetryPause = func() {
	base := 5 * time.Second
	time.Sleep(base + rand.N(base/2))
}

// maxDrain bounds the read below. The largest answer liken asks for
// is a fleet-wide list of Machines, and the caller decodes that whole
// list into memory anyway, so reading the tail of one costs nothing
// the pass was not already paying. Past this size, the connection is
// the cheaper thing to lose.
const maxDrain = 4 << 20

// drain reads whatever the caller left in the response body, then
// closes it. Go hands a connection back to its pool only when the
// body reaches EOF. A body closed before then costs the connection
// two ways, and both are invisible from this side, because the caller
// already has its answer. The next request pays for a fresh TCP
// connection and TLS handshake. And the socket closing while the API
// server is still finishing the request arrives there as a hang-up,
// so the server abandons the request it just answered and logs it as
// aborted.
//
// Reading to EOF matters most on the paths that want nothing back. A
// PUT to a status subresource answers with the whole updated object,
// which is tens of kilobytes that no caller here reads. A 404 and a
// 409 each answer with a Status object. None of those bodies ends on
// its own.
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}

// ErrNotFound marks the difference between "this object does not
// exist" and a real failure. An absent object is a normal state, and
// the caller handles it by creating the object. ErrConflict marks the
// same difference for "something else wrote to the object first".
// This is a normal state under optimistic concurrency, and the
// caller handles it by reading the object again.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict: something else wrote this object first")
)

// PatchJSON applies a JSON merge patch (RFC 7386). The caller sends
// only the fields to change, and the server merges them into the
// object (a null value deletes a key). This method skips optimistic
// concurrency on purpose: a merge patch carries no resourceVersion,
// so it cannot conflict. Use this method when the caller owns the
// specific fields it changes, such as a cordon flag or an
// annotation, and does not need to check the rest of the object.
func (c *Client) PatchJSON(path string, patch []byte) error {
	resp, err := c.Do(http.MethodPatch, path, "application/merge-patch+json", patch)
	if err != nil {
		return err
	}
	defer drain(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("PATCH %s: %s: %s", path, resp.Status, message)
	}
	return nil
}

// RequestJSON sends a request and decodes the JSON response into out.
// It turns any non-2xx status into an error that includes the
// server's response body. Kubernetes API errors are structured JSON
// with a message field, but the raw body is readable enough for this
// purpose.
func (c *Client) RequestJSON(method, path string, body []byte, out any) error {
	contentType := ""
	if body != nil {
		contentType = "application/json"
	}
	resp, err := c.Do(method, path, contentType, body)
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

// List sends a GET request for a collection, and unwraps the envelope
// that every Kubernetes list response uses: a <Kind>List object whose
// items field holds the collection. Callers get the items themselves
// and never see the wrapping object.
func List[T any](c *Client, path string) ([]T, error) {
	var list struct {
		Items []T `json:"items"`
	}
	if err := c.RequestJSON(http.MethodGet, path, nil, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// get sends a GET request for a single object. Unlike lists, single
// objects arrive with no envelope, so this function adds only the
// allocation and the error shape.
func get[T any](c *Client, path string) (*T, error) {
	out := new(T)
	if err := c.RequestJSON(http.MethodGet, path, nil, out); err != nil {
		return nil, err
	}
	return out, nil
}
