// Package kubernetes lets liken's controllers communicate with the
// Kubernetes API. It provides access to liken's own resources, the
// heartbeat-lease protocol, and pod eviction. The client that sends
// each request is the apiclient package of the shared module
// kubernetes/, at the top of the repository, which the other operators
// use too. The watches are in that module's informer package.
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
// watch open and recovers it when the stream drops (the shared
// informer package says why). The liken CLI imports this package and
// watches nothing, so this package imports no watch, and the CLI
// links no client-go.
package kubernetes

// From the pod's credentials, the API is plain REST. Every object
// lives at a predictable URL (/apis/<group>/<version>/<plural>/<name>).
// A GET request reads it. A POST request creates it. A PUT request
// replaces it. Authentication uses one bearer-token header. The
// command kubectl -v=9 prints these exact requests, and the shared
// client sends the same requests directly.

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/liken/api"
)

// serviceAccountDir names the path where kubelet mounts each
// container's API credentials. It is a variable so tests can point
// it at a directory they control, the same seam init's disk code
// leaves open with sysBlock and devRoot.
var serviceAccountDir = apiclient.ServiceAccountDir

// MachinesPath and ClustersPath name the URLs where our CRDs' objects
// live. Every resource in Kubernetes uses the same URL structure.
// Built-in resources use the legacy /api/v1 root instead of
// /apis/<group>. Both Machines and Clusters are cluster-scoped kinds,
// so their URLs have no /namespaces/<ns>/ segment.
const (
	MachinesPath = "/apis/" + api.APIVersion + "/machines"
	ClustersPath = "/apis/" + api.APIVersion + "/clusters"
)

// InClusterClient builds an operator's client from the pod's
// ServiceAccount. server, when it is not empty, replaces the address
// that the environment names. The environment names the API
// service's virtual IP. That virtual IP uses iptables NAT: iptables
// pins every new connection to one API server, and a client cannot
// choose which server it uses. Ordinary pods accept this limit. But a
// hostNetwork pod running on a machine that also runs an API server
// (or k3s's health-checked local load balancer over all API servers)
// has a better address to use: its own loopback address, where a dead
// remote server can never strand a connection. The credentials stay
// the same in both cases.
//
// The client answers a 429 at once, with no wait. Each operator's loop
// runs again within ten seconds, and that loop is its retry. A pass
// that waited out a 429 would stretch past what the timing below
// assumes: the heartbeat Lease that a pass renews carries the time the
// pass started, and a Lost verdict or a reboot grant is written from
// what the sweep read when it started.
func InClusterClient(server string) (*apiclient.Client, error) {
	c, err := apiclient.InCluster(apiclient.InClusterOptions{
		ServiceAccountDir: serviceAccountDir,
		Server:            server,
		Timeout:           requestTimeout,
	})
	if err != nil {
		return nil, err
	}
	return c.WithWaitContext(noWait), nil
}

// noWait is a context that has already ended, for a client whose wait
// after a 429 must end at once.
var noWait = func() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}()

// requestTimeout bounds one request of an in-cluster client, from the
// dial to the last byte of the answer. The shared client's timeouts
// on the dial, the answer's headers, and an idle connection each
// limit a server that stops answering without a FIN or an RST, and
// this bound limits the whole request. Fifteen seconds keeps an
// unlucky pass that hits several dead connections in a row well
// inside the forty-second heartbeat window (see heartbeat.go): a
// client that stalls on another machine's failure must never make
// this machine appear dead. The bound matters most to the cluster
// operator: a write that its leader election allowed is abandoned by
// this client before a new leader can take the Lease
// (cluster-operator/leader.go gives the numbers).
const requestTimeout = 15 * time.Second

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

// PatchJSON applies a JSON merge patch (RFC 7386). The caller sends
// only the fields to change, and the server merges them into the
// object (a null value deletes a key). This method skips optimistic
// concurrency on purpose: a merge patch carries no resourceVersion,
// so it cannot conflict. Use this method when the caller owns the
// specific fields it changes, such as a cordon flag or an
// annotation, and does not need to check the rest of the object.
//
// The shared client answers a 404 and a 409 as bare errors, with no
// path, because most callers handle them as states. A patch of an
// object that does not exist, or one that conflicts, such as a taints
// patch that states the version it read, answers apiclient.ErrNotFound
// or apiclient.ErrConflict wrapped with the path, so a log line names
// the object. Every other failure already carries the method, the path,
// and the API server's own text.
func PatchJSON(c *apiclient.Client, path string, patch []byte) error {
	err := c.Request(http.MethodPatch, path, "application/merge-patch+json", patch, nil)
	if errors.Is(err, apiclient.ErrNotFound) || errors.Is(err, apiclient.ErrConflict) {
		return fmt.Errorf("PATCH %s: %w", path, err)
	}
	return err
}

// List sends a GET request for a collection, and unwraps the envelope
// that every Kubernetes list response uses: a <Kind>List object whose
// items field holds the collection. Callers get the items themselves
// and never see the wrapping object.
func List[T any](c *apiclient.Client, path string) ([]T, error) {
	var list struct {
		Items []T `json:"items"`
	}
	if err := c.RequestJSON(http.MethodGet, path, nil, &list); err != nil {
		return nil, err
	}
	return list.Items, nil
}
