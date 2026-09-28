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

// apiRequestTimeout bounds one request of the Client from the dial to
// the last byte of the body. Every request the Client sends is one
// read or one write, and none streams: the watches run on client-go's
// own client (watch.go). A pass that waits on a request waits at most
// this long, and the next wake or tick tries it again. It is a
// variable so a test holds it short.
var apiRequestTimeout = 30 * time.Second

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

// The three content types this client sends. A body is JSON,
// except an apply, which the API server reads as a partial object
// under the caller's field manager. The apply media type is named for
// YAML and accepts JSON, because YAML is its superset.
//
// The merge patch type writes one metadata field and leaves every other
// field of the object alone.
const (
	jsonContentType  = "application/json"
	applyContentType = "application/apply-patch+yaml"
	mergePatchType   = "application/merge-patch+json"
)

func (c *Client) send(method, path, contentType string, body []byte) (*http.Response, error) {
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
	resp, err := c.send(method, path, contentType, body)
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

// The collection paths. The watches read each collection through
// client-go (clusterwatch.go), and this client writes each object
// under its namespace's path, and under its group's path for a
// cluster-scoped kind.
const (
	playsPath       = "/apis/" + mediaAPIVersion + "/plays"
	playersPath     = "/apis/" + mediaAPIVersion + "/players"
	remotesAllPath  = "/apis/" + mediaAPIVersion + "/remotes"
	keymapsPath     = "/apis/" + mediaAPIVersion + "/keymaps"
	mediaPrefsPath  = "/apis/" + mediaAPIVersion + "/mediapreferences"
	mediaPrefix     = "/apis/" + mediaAPIVersion + "/namespaces/"
	claimPrefix     = "/apis/" + claimAPIVersion + "/namespaces/"
	slicesPath      = "/apis/" + claimAPIVersion + "/resourceslices"
	displaysPath    = "/apis/" + displayAPIVersion + "/displays"
	receiversPath   = "/apis/" + receiverAPIVersion + "/receivers"
	peripheralsPath = "/apis/" + peripheralAPIVersion + "/peripherals"
	podPrefix       = "/api/v1/namespaces/"
)

func playPath(namespace, name string) string {
	return mediaPrefix + namespace + "/plays/" + name
}

func playerPath(namespace, name string) string {
	return mediaPrefix + namespace + "/players/" + name
}

func remotesPath(namespace string) string {
	return mediaPrefix + namespace + "/remotes"
}

func remotePath(namespace, name string) string {
	return remotesPath(namespace) + "/" + name
}

func claimsPath(namespace string) string {
	return claimPrefix + namespace + "/resourceclaims"
}

func podsPath(namespace string) string {
	return podPrefix + namespace + "/pods"
}

func GetPlay(c *Client, namespace, name string) (*Play, error) {
	play := &Play{}
	if err := c.RequestJSON(http.MethodGet, playPath(namespace, name), nil, play); err != nil {
		return nil, err
	}
	return play, nil
}

// DeletePlay removes one Play once its window after finishing has passed.
// It is the one object a person created that this operator deletes, and it
// deletes it only in that one state. An already-absent Play is success,
// because a person may delete a Finished Play before its window ends.
func DeletePlay(c *Client, namespace, name string) error {
	err := c.RequestJSON(http.MethodDelete, playPath(namespace, name), nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// PatchPlayFinalizers writes one Play's finalizer list and answers the
// resourceVersion the write left behind. The resourceVersion the caller
// read the Play at rides in the patch, so a write another program made
// first answers ErrConflict rather than overwriting it. An absent Play
// answers ErrNotFound.
func PatchPlayFinalizers(c *Client, namespace, name, resourceVersion string, finalizers []string) (string, error) {
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"resourceVersion": resourceVersion,
			"finalizers":      finalizers,
		},
	})
	if err != nil {
		return "", err
	}
	patched := &Play{}
	if err := c.requestJSON(http.MethodPatch, playPath(namespace, name),
		mergePatchType, body, patched); err != nil {
		return "", err
	}
	return patched.Metadata.ResourceVersion, nil
}

// PutPlayerStatus writes the Player's status subresource, the one
// write path this operator has onto a Player.
func PutPlayerStatus(c *Client, player *Player) (*Player, error) {
	body, err := json.Marshal(player)
	if err != nil {
		return nil, err
	}
	written := &Player{}
	path := playerPath(player.Metadata.Namespace, player.Metadata.Name) + "/status"
	if err := c.RequestJSON(http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
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

// GetRemote reads one Remote by name in a namespace, the name a
// Player's spec.remotes entry carries.
func GetRemote(c *Client, namespace, name string) (*Remote, error) {
	remote := &Remote{}
	if err := c.RequestJSON(http.MethodGet, remotePath(namespace, name), nil, remote); err != nil {
		return nil, err
	}
	return remote, nil
}

// PutRemoteStatus writes the Remote's status subresource, the one write
// path this operator has onto a Remote. The subresource split keeps it
// from ever rewriting the device selector a person declared.
func PutRemoteStatus(c *Client, remote *Remote) (*Remote, error) {
	body, err := json.Marshal(remote)
	if err != nil {
		return nil, err
	}
	written := &Remote{}
	path := remotePath(remote.Metadata.Namespace, remote.Metadata.Name) + "/status"
	if err := c.RequestJSON(http.MethodPut, path, body, written); err != nil {
		return nil, err
	}
	return written, nil
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

// PatchPodLabels adds labels to one pod and leaves the labels it
// already carries alone, which is what a merge patch of
// metadata.labels does. The operator writes one label this way, the
// ending, so the patch names no other field of a pod the kubelet is
// running. An absent pod answers ErrNotFound, because a pod that has
// gone is nothing to label.
func PatchPodLabels(c *Client, namespace, name string, labels map[string]string) error {
	body, err := json.Marshal(map[string]any{
		"metadata": map[string]any{"labels": labels},
	})
	if err != nil {
		return err
	}
	return c.requestJSON(http.MethodPatch, podsPath(namespace)+"/"+name,
		mergePatchType, body, nil)
}

// DeletePod removes one playback pod. An already-absent pod is
// success, because the graceful recreate deletes the pod before it
// creates the replacement, and a delete that races another pass must
// not fail.
func DeletePod(c *Client, namespace, name string) error {
	err := c.RequestJSON(http.MethodDelete, podsPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// ApplyDisplayOverride writes spec.override and nothing else,
// under this operator's own field manager. A nil override applies an
// empty spec, and the API server then removes the block this manager
// owns, which is how the panel comes back.
func ApplyDisplayOverride(c *Client, name string, override *DisplayOverride) error {
	body, err := json.Marshal(&displayApply{
		APIVersion: displayAPIVersion,
		Kind:       "Display",
		Metadata:   ObjectMeta{Name: name},
		Spec:       DisplaySpec{Override: override},
	})
	if err != nil {
		return err
	}
	path := displaysPath + "/" + name + "?fieldManager=" + applyFieldManager
	return c.requestJSON(http.MethodPatch, path, applyContentType, body, nil)
}

// ApplyReceiverSession writes status.session and nothing else, under
// this operator's own field manager on the status subresource. A nil
// session applies an empty status, and the API server then removes the
// block this manager owns. That is how the equipment is released.
//
// The session is status and not spec because a status write changes no
// metadata.generation. The equipment operator reads a new generation as
// a new statement of its settings, so a session in spec made every
// active or awake flip send the settings again.
func ApplyReceiverSession(c *Client, name string, session *ReceiverSession) error {
	body, err := json.Marshal(&receiverStatusApply{
		APIVersion: receiverAPIVersion,
		Kind:       "Receiver",
		Metadata:   ObjectMeta{Name: name},
		Status:     receiverSessionStatus{Session: session},
	})
	if err != nil {
		return err
	}
	path := receiversPath + "/" + name + "/status?fieldManager=" + applyFieldManager
	return c.requestJSON(http.MethodPatch, path, applyContentType, body, nil)
}

// ReleaseReceiverSpecSession applies an empty spec under this
// operator's field manager, and the API server then removes the
// spec.session this manager owns. A field another manager owns stays,
// so the apply never removes the cluster owner's inputs or topics.
func ReleaseReceiverSpecSession(c *Client, name string) error {
	body, err := json.Marshal(&receiverSpecApply{
		APIVersion: receiverAPIVersion,
		Kind:       "Receiver",
		Metadata:   ObjectMeta{Name: name},
	})
	if err != nil {
		return err
	}
	path := receiversPath + "/" + name + "?fieldManager=" + applyFieldManager
	return c.requestJSON(http.MethodPatch, path, applyContentType, body, nil)
}

// DeleteResourceClaim removes one playback claim. The operator deletes
// a claim only when a Player reshaped it, so the recreate builds the
// claim the current Player produces. An already-absent claim is
// success.
func DeleteResourceClaim(c *Client, namespace, name string) error {
	err := c.RequestJSON(http.MethodDelete, claimsPath(namespace)+"/"+name, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
