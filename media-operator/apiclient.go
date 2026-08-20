package main

// This is a Kubernetes client written straight against the HTTP API,
// following liken's own (kubernetes/apiclient.go) and the audio
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

// Do sends one request and hands back the open response, which is
// what the watch needs and what RequestJSON is built on.
func (c *Client) Do(method, path string, body []byte) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.base+path, reader)
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
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// RequestJSON sends one request and decodes the answer, turning
// every non-2xx status into an error that carries the server's own
// message.
func (c *Client) RequestJSON(method, path string, body []byte, out any) error {
	resp, err := c.Do(method, path, body)
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

// drain reads whatever the caller left in the body, then closes it.
// Go returns a connection to its pool only when the body reaches
// EOF, so an early close costs a fresh connection and TLS handshake,
// and reaches the server as a hang-up on a request it answered.
const maxDrain = 4 << 20

func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}

// The collection paths. Plays are listed and watched across every
// namespace and read back per namespace, and the two Kubernetes
// kinds this operator writes live under their own groups' paths.
const (
	playsPath   = "/apis/" + mediaAPIVersion + "/plays"
	mediaPrefix = "/apis/" + mediaAPIVersion + "/namespaces/"
	claimPrefix = "/apis/" + claimAPIVersion + "/namespaces/"
	podPrefix   = "/api/v1/namespaces/"
)

func playPath(namespace, name string) string {
	return mediaPrefix + namespace + "/plays/" + name
}

func playerPath(namespace, name string) string {
	return mediaPrefix + namespace + "/players/" + name
}

// Remotes are listed per namespace, because a Play binds only to
// Remotes beside it; a Keymap is read one at a time, by the name a
// Remote carries.
func remotesPath(namespace string) string {
	return mediaPrefix + namespace + "/remotes"
}

func keymapPath(namespace, name string) string {
	return mediaPrefix + namespace + "/keymaps/" + name
}

func claimsPath(namespace string) string {
	return claimPrefix + namespace + "/resourceclaims"
}

func podsPath(namespace string) string {
	return podPrefix + namespace + "/pods"
}

// ListPlays answers a whole pass with one request, and the list's
// resourceVersion is where a watch resumes from.
func ListPlays(c *Client) (*PlayList, error) {
	list := &PlayList{}
	if err := c.RequestJSON(http.MethodGet, playsPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

func GetPlay(c *Client, namespace, name string) (*Play, error) {
	play := &Play{}
	if err := c.RequestJSON(http.MethodGet, playPath(namespace, name), nil, play); err != nil {
		return nil, err
	}
	return play, nil
}

func GetPlayer(c *Client, namespace, name string) (*Player, error) {
	player := &Player{}
	if err := c.RequestJSON(http.MethodGet, playerPath(namespace, name), nil, player); err != nil {
		return nil, err
	}
	return player, nil
}

// PutPlayStatus writes through the status subresource, which is its
// own write path: this request can never touch a spec. The
// resourceVersion in the body is what makes the write conditional.
func PutPlayStatus(c *Client, play *Play) (*Play, error) {
	body, err := json.Marshal(play)
	if err != nil {
		return nil, err
	}
	written := &Play{}
	path := playPath(play.Metadata.Namespace, play.Metadata.Name) + "/status"
	if err := c.RequestJSON(http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}

// ListRemotes reads the whole namespace's remotes in one request,
// because nothing indexes a Remote by the player it binds and the
// filter runs here.
func ListRemotes(c *Client, namespace string) (*RemoteList, error) {
	list := &RemoteList{}
	if err := c.RequestJSON(http.MethodGet, remotesPath(namespace), nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

func GetKeymap(c *Client, namespace, name string) (*Keymap, error) {
	keymap := &Keymap{}
	if err := c.RequestJSON(http.MethodGet, keymapPath(namespace, name), nil, keymap); err != nil {
		return nil, err
	}
	return keymap, nil
}

func GetResourceClaim(c *Client, namespace, name string) (*ResourceClaim, error) {
	claim := &ResourceClaim{}
	if err := c.RequestJSON(http.MethodGet, claimsPath(namespace)+"/"+name, nil, claim); err != nil {
		return nil, err
	}
	return claim, nil
}

func CreateResourceClaim(c *Client, claim *ResourceClaim) (*ResourceClaim, error) {
	body, err := json.Marshal(claim)
	if err != nil {
		return nil, err
	}
	created := &ResourceClaim{}
	if err := c.RequestJSON(http.MethodPost, claimsPath(claim.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

func GetPod(c *Client, namespace, name string) (*Pod, error) {
	pod := &Pod{}
	if err := c.RequestJSON(http.MethodGet, podsPath(namespace)+"/"+name, nil, pod); err != nil {
		return nil, err
	}
	return pod, nil
}

func CreatePod(c *Client, pod *Pod) (*Pod, error) {
	body, err := json.Marshal(pod)
	if err != nil {
		return nil, err
	}
	created := &Pod{}
	if err := c.RequestJSON(http.MethodPost, podsPath(pod.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}
