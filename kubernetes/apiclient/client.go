// Package apiclient sends an operator's reads and writes to the
// Kubernetes API server.
//
// Each request is HTTPS with a JSON body, so the client uses only
// net/http and encoding/json and imports nothing from k8s.io. That lets
// a program that must not link client-go, such as a pod build of an
// operator or a command-line tool, use it. Watches are in the informer
// package, which runs on client-go's reflector.
//
// In a pod, the API server's address comes from the
// KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT environment
// variables, and the kubelet mounts the CA certificate and the
// ServiceAccount token under ServiceAccountDir. InCluster reads the same
// variables and files as client-go's rest.InClusterConfig().
package apiclient

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
	"unicode"
)

// ServiceAccountDir is where the kubelet mounts a container's API
// credentials.
const ServiceAccountDir = "/var/run/secrets/kubernetes.io/serviceaccount"

// ErrNotFound is a 404. ErrConflict is a 409, which the API server
// returns when a write has an out-of-date resourceVersion or a create
// names an object that already exists. A caller usually handles either
// one: it creates the missing object, or reads the object again and
// retries the write.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict: the object changed since it was read, or it already exists")
)

// ErrForbidden is a 403 because RBAC does not grant the request. An
// operator whose ClusterRole comes from an older release can get it for
// a kind that the older ClusterRole does not grant, and can then treat
// the kind as absent. A 403 for a namespace that is being deleted is
// ErrNamespaceTerminating instead.
var ErrForbidden = errors.New("forbidden")

// ErrNamespaceTerminating is the 403 for a create in a namespace that is
// being deleted. The deletion removes everything in the namespace, so a
// caller can drop the write instead of retrying it.
var ErrNamespaceTerminating = errors.New("the namespace is being deleted")

// ErrThrottled is a 429 that the client stopped waiting out, either
// because the waits reached maxThrottleWait or because the wait context
// ended. A caller that must not give up, such as an operator's first
// read at start, retries until its own context ends.
var ErrThrottled = errors.New("throttled: the API server kept responding 429")

// Stale reports whether a write failed because the object it was based
// on has since been changed or deleted: ErrConflict or ErrNotFound.
func Stale(err error) bool {
	return errors.Is(err, ErrConflict) || errors.Is(err, ErrNotFound)
}

// Client sends requests to one API server. The With methods return
// copies that share its connections.
type Client struct {
	base        string
	http        *http.Client
	credentials string

	// ctx is set by WithContext. When it is nil, no context ends a
	// request.
	ctx context.Context

	// waitCtx is set by WithWaitContext. When it is nil, ctx ends the
	// wait after a 429.
	waitCtx context.Context

	writeGuard func() error
	observer   func(Outcome)
}

// Outcome is the final result of one request, as WithObserver reports
// it.
type Outcome struct {
	Method string
	Path   string

	// Status is the HTTP status of the last response, or zero when no
	// response came, such as when the connection failed, the context
	// ended, or a write guard refused the request.
	Status int

	// Err is the error the caller received, or nil.
	Err error
}

// New builds a client for the server at base. credentials is the
// directory that holds the file `token`, which the client sends as a
// bearer token. When credentials is empty, the client sends no token.
func New(base string, httpClient *http.Client, credentials string) *Client {
	return &Client{base: base, http: httpClient, credentials: credentials}
}

// InClusterOptions are the optional parts of an in-cluster client.
type InClusterOptions struct {
	// ServiceAccountDir holds ca.crt and token. Empty means
	// the package constant ServiceAccountDir.
	ServiceAccountDir string

	// Server is the API server's URL, such as https://127.0.0.1:6443.
	// Empty means the kubernetes Service address from the environment.
	// That address is a virtual IP, and iptables sends each new
	// connection to one API server that the client cannot choose. A pod
	// on the host network can set a loopback address here instead, when
	// its machine runs an API server or k3s's local load balancer listens
	// on that address. Then no connection goes to a remote server that
	// has died. The CA and the token are valid at both addresses.
	Server string

	// Timeout limits one request, from the dial to the last byte of the
	// response. Zero means defaultTimeout. A program with a deadline of
	// its own sets a shorter limit, such as a leader that must stop
	// writing before another replica can take its Lease.
	Timeout time.Duration
}

const defaultTimeout = 30 * time.Second

// InCluster builds a client from the pod's environment and
// ServiceAccount files.
func InCluster(options InClusterOptions) (*Client, error) {
	server := options.Server
	if server == "" {
		host, port := os.Getenv("KUBERNETES_SERVICE_HOST"), os.Getenv("KUBERNETES_SERVICE_PORT")
		if host == "" || port == "" {
			return nil, errors.New("not running in a cluster: KUBERNETES_SERVICE_HOST or KUBERNETES_SERVICE_PORT is not set")
		}
		server = "https://" + host + ":" + port
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	dir := options.ServiceAccountDir
	if dir == "" {
		dir = ServiceAccountDir
	}

	// Trust only the cluster's CA, not the system trust store, so the
	// client refuses any other server at the API server's address.
	caPEM, err := os.ReadFile(dir + "/ca.crt")
	if err != nil {
		return nil, fmt.Errorf("reading service account CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("%s/ca.crt contains no certificates", dir)
	}

	return New(server, &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: roots},
			// A machine that loses power or its network sends no FIN or
			// RST, so a connection to it stays open with nothing on it.
			// These timeouts limit how long each step waits on such a
			// connection.
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 10 * time.Second,
			}).DialContext,
			ResponseHeaderTimeout: 10 * time.Second,
			IdleConnTimeout:       30 * time.Second,
		},
		// The informer package runs every watch, so no request of this
		// client streams. That lets the limit cover the whole response
		// body, so a body that stops part way cannot block the caller.
		Timeout: timeout,
	}, dir), nil
}

// WithContext returns a client whose requests, and whose waits after a
// 429, end when ctx ends. It clears any context set by WithWaitContext.
func (c *Client) WithContext(ctx context.Context) *Client {
	bound := *c
	bound.ctx, bound.waitCtx = ctx, nil
	return &bound
}

// WithWaitContext returns a client whose wait after a 429 ends when ctx
// ends, and the request then returns ErrThrottled. ctx does not cancel
// a request that is already sent. A caller that cancels a sent write
// cannot tell whether the API server applied it, so a caller that needs
// the result uses this in place of WithContext. For example, an operator that
// releases its Lease on shutdown lets its sent writes finish, but does
// not wait out a 429.
func (c *Client) WithWaitContext(ctx context.Context) *Client {
	bound := *c
	bound.waitCtx = ctx
	return &bound
}

// WithWriteGuard returns a client that calls guard before each request
// that is not a GET, including each resend after a 429. When guard
// returns an error, the client does not send the request and returns
// that error. A leader uses it to stop writing when it can no longer
// confirm that it holds its Lease. The guard is in the client so that
// it covers every write through the client, including the status writes
// of the memo package.
func (c *Client) WithWriteGuard(guard func() error) *Client {
	guarded := *c
	guarded.writeGuard = guard
	return &guarded
}

// WithObserver returns a client that calls observe with the Outcome of
// each request, after any wait for a 429. A program can use it to see
// every failed write in one place, including a write whose caller only
// logs the error, and retry sooner than its next scheduled attempt.
func (c *Client) WithObserver(observe func(Outcome)) *Client {
	observed := *c
	observed.observer = observe
	return &observed
}

func (c *Client) waitContext() context.Context {
	if c.waitCtx != nil {
		return c.waitCtx
	}
	return c.context()
}

func (c *Client) context() context.Context {
	if c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

// RequestJSON sends one request with a JSON body and decodes the JSON
// response into out. A nil out discards the response body.
func (c *Client) RequestJSON(method, path string, body []byte, out any) error {
	return c.Request(method, path, "application/json", body, out)
}

// Request sends a body of the given content type. A PATCH needs it,
// because the API server reads the patch type (merge patch, JSON patch,
// or apply) from the Content-Type header.
//
// A 404 returns ErrNotFound, a 409 returns ErrConflict, and a 403
// returns ErrNamespaceTerminating or ErrForbidden. Any other status
// outside 2xx returns an error that includes the response body. After a
// 429, Request waits as long as the API server asks and sends the
// request again, until the total wait would pass maxThrottleWait.
//
// When out is a *[]byte, Request stores the raw response body in it, up
// to maxDrain bytes, for a response that is not JSON, such as a pod's
// log.
func (c *Client) Request(method, path, contentType string, body []byte, out any) error {
	status, err := c.request(method, path, contentType, body, out)
	if c.observer != nil {
		c.observer(Outcome{Method: method, Path: path, Status: status, Err: err})
	}
	return err
}

// request calls send again after each 429 that it can wait out, and
// returns the last HTTP status and error.
func (c *Client) request(method, path, contentType string, body []byte, out any) (int, error) {
	var waited time.Duration
	for {
		status, err := c.send(method, path, contentType, body, out)
		var throttled *throttledError
		if !errors.As(err, &throttled) || waited+throttled.wait > maxThrottleWait*time.Second {
			return status, err
		}
		timer := time.NewTimer(throttled.wait)
		select {
		case <-c.waitContext().Done():
			timer.Stop()
			return status, err
		case <-timer.C:
		}
		waited += throttled.wait
	}
}

// maxThrottleWait is the total number of seconds that one request waits
// across its 429 responses before it returns ErrThrottled. While the API
// server initializes the storage of a CRD that it has just received, it
// responds 429 with the message "storage is (re)initializing" for a
// second or two, so a rollout that changes a CRD gets that response and
// a resend succeeds. A server that keeps responding 429 for longer is
// overloaded, and the caller's own retry, which waits longer, handles it.
//
// A context from WithContext or WithWaitContext can end the wait
// sooner. Otherwise a request blocks its caller for up to this many
// seconds plus the timeout of each send. A caller that holds a lock
// across the request, such as memo.Versions.Send, holds it that long
// too, and so does a shutdown that waits on that caller.
const maxThrottleWait = 10

// send makes one attempt of a request and returns the HTTP status of
// the response, or zero when there was none.
func (c *Client) send(method, path, contentType string, body []byte, out any) (int, error) {
	if c.writeGuard != nil && method != http.MethodGet {
		if err := c.writeGuard(); err != nil {
			return 0, fmt.Errorf("%s %s was not sent: the write guard refused it: %w", method, path, err)
		}
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(c.context(), method, c.base+path, reader)
	if err != nil {
		return 0, err
	}
	if err := c.authorize(req); err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer drain(resp.Body)

	status := resp.StatusCode
	switch {
	case status == http.StatusNotFound:
		return status, ErrNotFound
	case status == http.StatusConflict:
		return status, ErrConflict
	case status < 200 || status > 299:
		message := responseText(resp.Body)
		if status == http.StatusForbidden && terminating(message) {
			return status, fmt.Errorf("%s %s: %w", method, path, ErrNamespaceTerminating)
		}
		err := fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, message)
		if status == http.StatusForbidden {
			return status, &forbiddenError{err: err}
		}
		if status == http.StatusTooManyRequests {
			seconds := retryAfter(resp.Header.Get("Retry-After"), message)
			return status, &throttledError{err: err, wait: time.Duration(max(seconds, 1)) * time.Second, seconds: seconds}
		}
		return status, err
	case out == nil:
		return status, nil
	}
	if text, raw := out.(*[]byte); raw {
		*text, err = io.ReadAll(io.LimitReader(resp.Body, maxDrain))
		return status, err
	}
	return status, json.NewDecoder(resp.Body).Decode(out)
}

// authorize adds the ServiceAccount token to a request. It reads the
// token file on every request, because the kubelet rewrites that file
// as each short-lived token nears its expiry, and a token cached in
// memory would eventually get 401 responses.
func (c *Client) authorize(req *http.Request) error {
	if c.credentials == "" {
		return nil
	}
	token, err := os.ReadFile(c.credentials + "/token")
	if err != nil {
		return fmt.Errorf("reading service account token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	return nil
}

// throttledError is a 429. seconds is the wait the response asked for,
// or zero when it gave none. wait is how long the client waits before
// it resends, which is one second when seconds is zero.
type throttledError struct {
	err     error
	wait    time.Duration
	seconds int
}

func (e *throttledError) Error() string { return e.err.Error() }

func (e *throttledError) Is(target error) bool { return target == ErrThrottled }

type forbiddenError struct{ err error }

func (e *forbiddenError) Error() string { return e.err.Error() }

func (e *forbiddenError) Is(target error) bool { return target == ErrForbidden }

// retryAfter returns the seconds that a 429 asks the client to wait,
// from the Retry-After header or else the details.retryAfterSeconds
// field of the Status body, or zero when it has neither.
func retryAfter(header, body string) int {
	var seconds int
	if _, err := fmt.Sscan(header, &seconds); err == nil && seconds > 0 {
		return seconds
	}
	var status struct {
		Details struct {
			RetryAfterSeconds int `json:"retryAfterSeconds"`
		} `json:"details"`
	}
	if json.Unmarshal([]byte(body), &status) == nil && status.Details.RetryAfterSeconds > 0 {
		return status.Details.RetryAfterSeconds
	}
	return 0
}

// RetryAfterSeconds returns the wait, in seconds, that the 429 inside
// err asked for. It returns zero when the 429 gave no wait or err does
// not wrap a 429. A caller that retries after ErrThrottled waits at least that
// long. A 429 with no wait is often a refusal that waiting does not
// clear, such as an eviction that a PodDisruptionBudget blocks, so the
// caller decides when to retry.
func RetryAfterSeconds(err error) int {
	var throttled *throttledError
	if !errors.As(err, &throttled) {
		return 0
	}
	return throttled.seconds
}

// responseText returns the start of a response body, for an error
// message. It trims the trailing newline that the API server adds to a
// Status body, so the error and the log line that prints it stay on one
// line.
func responseText(body io.Reader) string {
	message, _ := io.ReadAll(io.LimitReader(body, 2048))
	return strings.TrimRightFunc(string(message), unicode.IsSpace)
}

// terminating reports whether a 403's Status body has the cause
// NamespaceTerminating. The cause is a stable field of the API, but the
// message beside it can change between releases.
func terminating(message string) bool {
	var status struct {
		Details struct {
			Causes []struct {
				Reason string `json:"reason"`
			} `json:"causes"`
		} `json:"details"`
	}
	_ = json.Unmarshal([]byte(message), &status)
	for _, cause := range status.Details.Causes {
		if cause.Reason == "NamespaceTerminating" {
			return true
		}
	}
	return false
}

// maxDrain is the most that drain reads of what remains of a body, and
// the most that a *[]byte target receives. Past this size, closing the
// connection costs less than reading the rest.
const maxDrain = 4 << 20

// drain reads what remains of a response body and closes it. Go
// returns a connection to its pool only when the body reaches EOF. A
// body closed early costs a new TCP connection and TLS handshake on the
// next request. Callers leave the body of a 404, a 409, or a response
// to a request with a nil out unread, so drain must read it.
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}
