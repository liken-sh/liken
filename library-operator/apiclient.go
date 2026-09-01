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

// The path the kubelet mounts the ServiceAccount credentials on.
const defaultServiceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// ServiceAccountDir is a variable so a test points it at a directory
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
func (c *Client) RequestJSON(ctx context.Context, method, path string, body []byte, out any) error {
	return c.RequestWithType(ctx, method, path, jsonContentType, body, out)
}

// The two content types this client sends. A PATCH needs its own,
// because the API server reads which patch dialect a request speaks
// from the Content-Type header alone.
const (
	jsonContentType = "application/json"
	mergePatchType  = "application/merge-patch+json"
)

// RequestWithType is RequestJSON with the request's own content type
// stated, which is what a merge patch needs.
func (c *Client) RequestWithType(ctx context.Context, method, path, contentType string, body []byte, out any) error {
	resp, err := c.do(ctx, method, path, contentType, body)
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
//
// The context is the caller's, so a pass that ends takes its requests
// with it, and a watch runs for as long as its own context does.
func (c *Client) Do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	return c.do(ctx, method, path, jsonContentType, body)
}

// Do is Do with the body's content type stated, so one request path
// sends both a JSON write and a merge patch.
func (c *Client) do(ctx context.Context, method, path, contentType string, body []byte) (*http.Response, error) {
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

// Drain reads whatever the caller left in the body, then closes it.
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

func ServerVersion(ctx context.Context, client *Client) (Version, error) {
	var version Version
	if err := client.RequestJSON(ctx, http.MethodGet, versionPath, nil, &version); err != nil {
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
	// The Catalogs, listed and watched across every namespace and
	// written back per namespace, the same shape as the Libraries.
	catalogsPath = "/apis/" + libraryAPIVersion + "/catalogs"
	// The Players, listed and watched across every namespace,
	// read-only. A Player is media-operator's object, and this operator
	// reads the collection to find the screens delegated to it.
	playersPath = "/apis/" + playerAPIVersion + "/players"

	libraryPrefix = "/apis/" + libraryAPIVersion + "/namespaces/"
	corePrefix    = "/api/v1/namespaces/"
	volumesPath   = "/api/v1/persistentvolumes"
	podsAllPath   = "/api/v1/pods"

	// The slices behind the catalog Services, one in every namespace that
	// holds a Library.
	endpointSlicePrefix = "/apis/" + endpointSliceAPIVersion + "/namespaces/"
)

// ScannerPodsQuery narrows a pod list or a pod watch to this
// operator's own scanner pods, by the name label every scanner pod
// carries. The equals sign inside the selector is percent-encoded, so
// the server reads one parameter and not two.
const scannerPodsQuery = "labelSelector=" + scannerLabelKey + "%3D" + scannerLabelValue

// The same narrowing for the screen pods, which carry a name label of
// their own. The two selectors keep the two kinds of pod apart, so a list of
// one never answers with the other.
const screenPodsQuery = "labelSelector=" + scannerLabelKey + "%3D" + screenLabelValue

func libraryPath(namespace, name string) string {
	return libraryPrefix + namespace + "/libraries/" + name
}

func catalogPath(namespace, name string) string {
	return libraryPrefix + namespace + "/catalogs/" + name
}

func claimPath(namespace, name string) string {
	return corePrefix + namespace + "/persistentvolumeclaims/" + name
}

func claimsPath(namespace string) string {
	return corePrefix + namespace + "/persistentvolumeclaims"
}

func podsPath(namespace string) string {
	return corePrefix + namespace + "/pods"
}

func endpointSlicesPath(namespace string) string {
	return endpointSlicePrefix + namespace + "/endpointslices"
}

func servicesPath(namespace string) string {
	return corePrefix + namespace + "/services"
}

// ListLibraries answers a whole pass with one request, and the list's
// resourceVersion is where the libraries watch resumes from.
func ListLibraries(ctx context.Context, c *Client) (*LibraryList, error) {
	list := &LibraryList{}
	if err := c.RequestJSON(ctx, http.MethodGet, librariesPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// PutLibraryStatus writes through the status subresource, which is its
// own write path: this request can never touch a spec. The
// resourceVersion in the body is what makes the write conditional, so
// a status written over a Library that changed underneath answers
// ErrConflict and the next pass reads it again.
func PutLibraryStatus(ctx context.Context, c *Client, library *Library) (*Library, error) {
	body, err := json.Marshal(library)
	if err != nil {
		return nil, err
	}
	written := &Library{}
	path := libraryPath(library.Metadata.Namespace, library.Metadata.Name) + "/status"
	if err := c.RequestJSON(ctx, http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}

// PatchLibraryFinalizers writes a Library's finalizer list and
// answers with the resourceVersion the write produced, which a
// caller needs before it writes the same object again in one pass.
//
// It is a merge patch and not a replace, because a replace sends
// every field this program models and drops every field it does not,
// which would take a person's own labels and annotations off the
// Library. The resourceVersion inside the patch makes the write
// conditional the same way a replace is: a write that raced another
// answers ErrConflict instead of clobbering it.
func PatchLibraryFinalizers(ctx context.Context, c *Client, namespace, name, resourceVersion string, finalizers []string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"resourceVersion": resourceVersion,
			"finalizers":      finalizers,
		},
	})
	if err != nil {
		return "", err
	}
	var patched struct {
		Metadata ObjectMeta `json:"metadata"`
	}
	path := libraryPath(namespace, name)
	if err := c.RequestWithType(ctx, http.MethodPatch, path, mergePatchType, body, &patched); err != nil {
		return "", err
	}
	return patched.Metadata.ResourceVersion, nil
}

// ListPlayers reads every Player in the cluster with one request, the
// way the pass reads the Libraries. The list's resourceVersion is where the
// player watch resumes from. A cluster with no media-operator serves no such
// collection, and the failure is the caller's to report and carry on from.
func ListPlayers(ctx context.Context, c *Client) (*PlayerList, error) {
	list := &PlayerList{}
	if err := c.RequestJSON(ctx, http.MethodGet, playersPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// ListCatalogs answers a whole pass with one request, and the list's
// resourceVersion is where the catalogs watch resumes from.
func ListCatalogs(ctx context.Context, c *Client) (*CatalogList, error) {
	list := &CatalogList{}
	if err := c.RequestJSON(ctx, http.MethodGet, catalogsPath, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// PutCatalogStatus writes through the status subresource, so this
// request can never touch a spec. The resourceVersion in the body makes
// the write conditional, the same as PutLibraryStatus.
func PutCatalogStatus(ctx context.Context, c *Client, catalog *NamespaceCatalog) (*NamespaceCatalog, error) {
	body, err := json.Marshal(catalog)
	if err != nil {
		return nil, err
	}
	written := &NamespaceCatalog{}
	path := catalogPath(catalog.Metadata.Namespace, catalog.Metadata.Name) + "/status"
	if err := c.RequestJSON(ctx, http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}

// GetPersistentVolumeClaim reads the claim a Library names, for two
// answers: whether it is bound, and which volume it is bound to. An
// absent claim is ErrNotFound, which the pass reports as the
// ClaimNotFound reason rather than as a failure. It also reads the
// catalog claim the operator provisions, to tell an existing one from
// none.
func GetPersistentVolumeClaim(ctx context.Context, c *Client, namespace, name string) (*PersistentVolumeClaim, error) {
	claim := &PersistentVolumeClaim{}
	if err := c.RequestJSON(ctx, http.MethodGet, claimPath(namespace, name), nil, claim); err != nil {
		return nil, err
	}
	return claim, nil
}

// CreatePersistentVolumeClaim provisions the catalog claim one scanner
// pod mounts. The operator creates it once and never updates it,
// because a claim's spec is immutable once it binds.
func CreatePersistentVolumeClaim(ctx context.Context, c *Client, claim *PersistentVolumeClaim) (*PersistentVolumeClaim, error) {
	body, err := json.Marshal(claim)
	if err != nil {
		return nil, err
	}
	created := &PersistentVolumeClaim{}
	if err := c.RequestJSON(ctx, http.MethodPost, claimsPath(claim.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// GetPersistentVolume reads the volume behind a bound claim, for what
// serves it. A PersistentVolume is cluster-scoped, so the path carries
// no namespace.
func GetPersistentVolume(ctx context.Context, c *Client, name string) (*PersistentVolume, error) {
	volume := &PersistentVolume{}
	if err := c.RequestJSON(ctx, http.MethodGet, volumesPath+"/"+name, nil, volume); err != nil {
		return nil, err
	}
	return volume, nil
}

// ListScannerPods reads this operator's scanner pods across every
// namespace, because a Library lives in whatever namespace its claim
// does. The list's resourceVersion is where the pod watch begins.
func ListScannerPods(ctx context.Context, c *Client) (*PodList, error) {
	list := &PodList{}
	if err := c.RequestJSON(ctx, http.MethodGet, podsAllPath+"?"+scannerPodsQuery, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

// ListScreenPods reads this operator's screen pods across every
// namespace, on the same terms as the scanner pods. The catalog EndpointSlice
// carries both kinds, because a screen's catalog agent is a peer of the
// namespace's cluster like a scanner's.
func ListScreenPods(ctx context.Context, c *Client) (*PodList, error) {
	list := &PodList{}
	if err := c.RequestJSON(ctx, http.MethodGet, podsAllPath+"?"+screenPodsQuery, nil, list); err != nil {
		return nil, err
	}
	return list, nil
}

func GetPod(ctx context.Context, c *Client, namespace, name string) (*Pod, error) {
	pod := &Pod{}
	if err := c.RequestJSON(ctx, http.MethodGet, podsPath(namespace)+"/"+name, nil, pod); err != nil {
		return nil, err
	}
	return pod, nil
}

func CreatePod(ctx context.Context, c *Client, pod *Pod) (*Pod, error) {
	body, err := json.Marshal(pod)
	if err != nil {
		return nil, err
	}
	created := &Pod{}
	if err := c.RequestJSON(ctx, http.MethodPost, podsPath(pod.Metadata.Namespace), body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// DeletePod removes one scanner pod. An already-absent pod is success,
// because the operator deletes a pod to replace it and a delete that
// races another pass must not fail.
func DeletePod(ctx context.Context, c *Client, namespace, name string) error {
	err := c.RequestJSON(ctx, http.MethodDelete, podsPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// GetService reads the live catalog Service of one namespace. The
// read answers the fields the operator compares, and it answers the
// resourceVersion and the addresses the API server assigned, which
// the update carries back unchanged.
func GetService(ctx context.Context, c *Client, namespace, name string) (*Service, error) {
	service := &Service{}
	if err := c.RequestJSON(ctx, http.MethodGet, servicesPath(namespace)+"/"+name, nil, service); err != nil {
		return nil, err
	}
	return service, nil
}

func CreateService(ctx context.Context, c *Client, service *Service) (*Service, error) {
	body, err := json.Marshal(service)
	if err != nil {
		return nil, err
	}
	created := &Service{}
	path := servicesPath(service.Metadata.Namespace)
	if err := c.RequestJSON(ctx, http.MethodPost, path, body, created); err != nil {
		return nil, err
	}
	return created, nil
}

// UpdateService writes the whole Service back. The resourceVersion in
// the body makes the write conditional, so a Service that changed
// underneath answers ErrConflict, and the next pass reads it again.
func UpdateService(ctx context.Context, c *Client, service *Service) (*Service, error) {
	body, err := json.Marshal(service)
	if err != nil {
		return nil, err
	}
	written := &Service{}
	path := servicesPath(service.Metadata.Namespace) + "/" + service.Metadata.Name
	if err := c.RequestJSON(ctx, http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
}
