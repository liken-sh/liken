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
// certificate and a ServiceAccount token at a known path.

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

// The path the kubelet mounts the ServiceAccount credentials on.
const defaultServiceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// serviceAccountDir is a variable so a test points it at a directory
// it controls.
var serviceAccountDir = defaultServiceAccountDir

// These two answers are values, not failures. An absent object is the
// normal state the caller answers by creating it, and a conflict is
// the normal state under optimistic concurrency that the caller
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
			// overall client timeout, because a watch is a request
			// whose response never ends, and a whole-request deadline
			// would cut every stream on schedule.
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 10 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		},
	}, serviceAccountDir), nil
}

// RequestJSON sends one request and decodes the answer, turning every
// non-2xx status into an error that carries the server's own message.
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

// Do sends one request and hands back the open response, which is
// what a watch needs and what RequestJSON is built on.
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

// drain reads whatever the caller left in the body, then closes it.
// Go returns a connection to its pool only when the body reaches
// EOF, so an early close costs a fresh connection and TLS handshake,
// and reaches the server as a hang-up on a request it answered.
const maxDrain = 4 << 20

func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}

// Every API server answers /version, and the answer needs no RBAC
// rule, so it is the cheapest proof that the client reached the
// server it was configured for.
const versionPath = "/version"

// Version holds the one field of /version the operator reports.
type Version struct {
	GitVersion string `json:"gitVersion"`
}

func ServerVersion(client *Client) (Version, error) {
	var version Version
	if err := client.RequestJSON(http.MethodGet, versionPath, nil, &version); err != nil {
		return Version{}, err
	}
	return version, nil
}

// The collection paths. Libraries are listed and watched across every
// namespace and written back per namespace. The storage and the pods
// are ordinary core-group objects: a claim and a pod are namespaced,
// and a volume is not.
const (
	librariesPath = "/apis/" + libraryAPIVersion + "/libraries"
	libraryPrefix = "/apis/" + libraryAPIVersion + "/namespaces/"
	corePrefix    = "/api/v1/namespaces/"
	volumesPath   = "/api/v1/persistentvolumes"
	podsAllPath   = "/api/v1/pods"

	// The slice behind the catalog Service, written in the operator's
	// own namespace and nowhere else.
	endpointSlicePrefix = "/apis/" + endpointSliceAPIVersion + "/namespaces/"
)

// scannerPodsQuery narrows a pod list or a pod watch to this
// operator's own scanner pods, by the name label every scanner pod
// carries. The equals sign inside the selector is percent-encoded, so
// the server reads one parameter and not two.
const scannerPodsQuery = "labelSelector=" + scannerLabelKey + "%3D" + scannerLabelValue

func libraryPath(namespace, name string) string {
	return libraryPrefix + namespace + "/libraries/" + name
}

func claimPath(namespace, name string) string {
	return corePrefix + namespace + "/persistentvolumeclaims/" + name
}

func podsPath(namespace string) string {
	return corePrefix + namespace + "/pods"
}

func endpointSlicesPath(namespace string) string {
	return endpointSlicePrefix + namespace + "/endpointslices"
}

// ListLibraries answers a whole pass with one request, and the list's
// resourceVersion is where the libraries watch resumes from.
func ListLibraries(c *Client) (*LibraryList, error) {
	list := &LibraryList{}
	if err := c.RequestJSON(http.MethodGet, librariesPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// PutLibraryStatus writes through the status subresource, which is its
// own write path: this request can never touch a spec. The
// resourceVersion in the body is what makes the write conditional, so
// a status written over a Library that changed underneath answers
// ErrConflict and the next pass reads it again.
func PutLibraryStatus(c *Client, library *Library) (*Library, error) {
	body, err := json.Marshal(library)
	if err != nil {
		return nil, err
	}
	written := &Library{}
	path := libraryPath(library.Metadata.Namespace, library.Metadata.Name) + "/status"
	if err := c.RequestJSON(http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}

// GetPersistentVolumeClaim reads the claim a Library names, for two
// answers: whether it is bound, and which volume it is bound to. An
// absent claim is ErrNotFound, which the pass reports as the
// ClaimNotFound reason rather than as a failure.
func GetPersistentVolumeClaim(c *Client, namespace, name string) (*PersistentVolumeClaim, error) {
	claim := &PersistentVolumeClaim{}
	if err := c.RequestJSON(http.MethodGet, claimPath(namespace, name), nil, claim); err != nil {
		return nil, err
	}
	return claim, nil
}

// GetPersistentVolume reads the volume behind a bound claim, for what
// serves it. A PersistentVolume is cluster-scoped, so the path carries
// no namespace.
func GetPersistentVolume(c *Client, name string) (*PersistentVolume, error) {
	volume := &PersistentVolume{}
	if err := c.RequestJSON(http.MethodGet, volumesPath+"/"+name, nil, volume); err != nil {
		return nil, err
	}
	return volume, nil
}

// ListScannerPods reads this operator's scanner pods across every
// namespace, because a Library lives in whatever namespace its claim
// does. The list's resourceVersion is where the pod watch begins.
func ListScannerPods(c *Client) (*PodList, error) {
	list := &PodList{}
	if err := c.RequestJSON(http.MethodGet, podsAllPath+"?"+scannerPodsQuery, nil, list); err != nil {
		return nil, err
	}
	return list, nil
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

// DeletePod removes one scanner pod. An already-absent pod is success,
// because the operator deletes a pod to replace it and a delete that
// races another pass must not fail.
func DeletePod(c *Client, namespace, name string) error {
	err := c.RequestJSON(http.MethodDelete, podsPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// GetEndpointSlice reads the live catalog slice, for the endpoints it
// holds now and for the resourceVersion the write is made conditional
// on. An absent slice is ErrNotFound, which the pass answers by
// creating one.
func GetEndpointSlice(c *Client, namespace, name string) (*EndpointSlice, error) {
	slice := &EndpointSlice{}
	if err := c.RequestJSON(http.MethodGet, endpointSlicesPath(namespace)+"/"+name, nil, slice); err != nil {
		return nil, err
	}
	return slice, nil
}

func CreateEndpointSlice(c *Client, slice *EndpointSlice) (*EndpointSlice, error) {
	body, err := json.Marshal(slice)
	if err != nil {
		return nil, err
	}
	created := &EndpointSlice{}
	path := endpointSlicesPath(slice.Metadata.Namespace)
	if err := c.RequestJSON(http.MethodPost, path, body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateEndpointSlice writes the whole slice back. The resourceVersion
// in the body makes the write conditional, so a slice that changed
// underneath answers ErrConflict, and the next pass reads it again.
func UpdateEndpointSlice(c *Client, slice *EndpointSlice) (*EndpointSlice, error) {
	body, err := json.Marshal(slice)
	if err != nil {
		return nil, err
	}
	written := &EndpointSlice{}
	path := endpointSlicesPath(slice.Metadata.Namespace) + "/" + slice.Metadata.Name
	if err := c.RequestJSON(http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}
