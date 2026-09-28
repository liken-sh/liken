package main

// The one call this program makes against the Kubernetes API server.
//
// The operator has a client of its own (apiclient.go in the
// repository root) that reads the same three things: the in-cluster
// address from the environment, the CA the kubelet mounts, and the
// ServiceAccount token beside it. The two do not share one copy,
// because both programs are package main and Go cannot import a main
// package. This copy stays as small as the one call it makes: a GET of
// the bond Secrets under one label, with absence separated from
// failure.

import (
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

// errNotFound marks the difference between "this object does not
// exist" and a real failure. A list of one adapter's Secrets answers
// with an empty list rather than a 404, and main.go reads that empty
// list as an adapter that has paired nothing. Any other failure is a
// set of keys this program could not read, and the pod must not start
// bluetoothd on an empty tree after one.
var errNotFound = errors.New("not found")

type apiClient struct {
	base        string
	http        *http.Client
	credentials string
}

// newAPIClient builds a client from its three parts. inClusterClient
// gets them from the pod's environment, and tests get them from an
// httptest server.
func newAPIClient(base string, httpClient *http.Client, credentials string) *apiClient {
	return &apiClient{base: base, http: httpClient, credentials: credentials}
}

func inClusterClient() (*apiClient, error) {
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

	return newAPIClient("https://"+host+":"+port, &http.Client{
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

// get reads one object and decodes it into out.
func (c *apiClient) get(path string, out any) error {
	req, err := http.NewRequest(http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	// The token is read from disk at the request rather than held in
	// memory. The kubelet refreshes the mounted file as each
	// short-lived token nears its expiry.
	if c.credentials != "" {
		token, err := os.ReadFile(c.credentials + "/token")
		if err != nil {
			return fmt.Errorf("reading service account token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+string(token))
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer drain(resp.Body)

	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("GET %s: %s: %s", path, resp.Status, message)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// maxDrain bounds the read below. The largest answer this program asks
// for is one adapter's bond Secrets, which the caller decodes into
// memory anyway, so reading the tail costs nothing new.
const maxDrain = 4 << 20

// drain reads whatever the caller left in the response body, then
// closes it. Go hands a connection back to its pool only when the body
// reaches EOF, and this program exits right after, so the cost of
// skipping it is a hang-up on a request the API server already
// answered.
func drain(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, io.LimitReader(body, maxDrain))
	_ = body.Close()
}
