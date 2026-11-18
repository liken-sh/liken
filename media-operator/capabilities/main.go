// media-capabilities is the agent that lets a claim ask for a GPU by
// what its media driver can decode, encode, and scale. One pod runs on
// each node with a GPU.
//
// `liken` publishes each render node as a `liken.sh` device with the
// facts it reads from the kernel. It cannot publish what a media driver
// supports, because that takes a VA-API query. This agent holds a
// shareable claim on every render node of its node, queries the VA-API
// driver of each one, and publishes one `media.liken.sh` device per
// render node. A workload's claim pairs that device with the `liken.sh`
// device of the same GPU through the standard attribute
// `resource.kubernetes.io/pciBusID`, so the scheduler allocates a
// render node only on a GPU whose driver supports what the claim asks
// for.
//
// The agent is a separate program from the operator because the query
// loads libva, so its image builds on the `vaapi` base, and the
// operator's static image has no libva. The agent also links a gRPC
// server for the kubelet's DRA plugin API, which the operator's program,
// run by every playback pod, does not need.
//
// With the argument `query <render node>`, the program runs one query
// and writes the report as JSON (query.go). With no argument, it runs
// the agent.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == queryMode {
		os.Exit(runQuery(os.Args[2:], os.Stdout, os.Stderr))
	}
	if err := runAgent(); err != nil {
		fmt.Fprintf(os.Stderr, "capabilities: %v\n", err)
		os.Exit(1)
	}
}

// runAgent builds the agent from the pod's environment and runs it
// until the pod receives SIGTERM or an interrupt. A failure before the
// first pass ends the process, and the kubelet restarts the container.
func runAgent() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	env, err := downwardEnv(os.Getenv)
	if err != nil {
		return err
	}
	client, err := apiclient.InCluster(apiclient.InClusterOptions{})
	if err != nil {
		return err
	}
	watches, err := informer.InCluster()
	if err != nil {
		return err
	}
	querier, err := selfQuerier()
	if err != nil {
		return err
	}
	a, err := newAgent(client, env, querier.query)
	if err != nil {
		return err
	}
	likenSlice := sliceName(a.node, likenDriver)
	watch := func(ctx context.Context, seen func(*ResourceSlice)) <-chan struct{} {
		return informer.WatchOne(ctx, watches, informer.One{
			Resource: schema.GroupVersionResource{Group: "resource.k8s.io", Version: "v1", Resource: "resourceslices"},
			Name:     likenSlice,
			What:     "the ResourceSlice " + likenSlice,
		}, seen, nil).Done()
	}
	return operate(ctx, a, watch, serveDRAPlugin)
}

// newAgent reads what stays fixed for the life of the pod: the Node
// that owns the slice, and the render nodes that the pod's claim holds.
func newAgent(client *apiclient.Client, env map[string]string,
	query func(context.Context, string) (report, error)) (*agent, error) {
	owner, err := nodeOwner(client, env["NODE_NAME"])
	if err != nil {
		return nil, fmt.Errorf("reading Node %s: %w", env["NODE_NAME"], err)
	}
	held, err := heldDevices(client, env["POD_NAMESPACE"], env["POD_NAME"])
	if err != nil {
		return nil, err
	}
	return &agent{
		client: client, node: env["NODE_NAME"], owner: owner,
		pod: env["POD_NAME"], namespace: env["POD_NAMESPACE"],
		held: held, reports: map[string]report{},
		query: query, renderNode: renderNodePath,
	}, nil
}

// operate serves the kubelet plugin and runs a pass for each version of
// the node's `liken.sh` slice, until the context ends or the plugin
// fails. watch calls its callback with each version of the slice, and
// with nil while the slice does not exist. The channel it returns
// closes after its last call.
func operate(ctx context.Context, a *agent,
	watch func(context.Context, func(*ResourceSlice)) <-chan struct{},
	serve func(context.Context) error) error {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	plugin := make(chan error, 1)
	go func() { plugin <- serve(ctx) }()

	// The pass always reads the newest version, so a burst of versions
	// needs only one pass.
	var mu sync.Mutex
	var latest *ResourceSlice
	wakes := make(chan struct{}, 1)
	watched := watch(ctx, func(slice *ResourceSlice) {
		mu.Lock()
		latest = slice
		mu.Unlock()
		wake(wakes)
	})

	passes := make(chan struct{})
	go func() {
		defer close(passes)
		runPasses(ctx, wakes, func(ctx context.Context) error {
			mu.Lock()
			slice := latest
			mu.Unlock()
			return a.pass(ctx, slice)
		})
	}()

	var failed error
	select {
	case <-ctx.Done():
	case err := <-plugin:
		failed = fmt.Errorf("serving the DRA plugin: %w", err)
		stop()
	}
	<-passes
	<-watched
	return failed
}

// downwardEnv reads the node name, pod name, and pod namespace that the
// DaemonSet sets from the downward API. It fails on the first one that
// is empty.
func downwardEnv(getenv func(string) string) (map[string]string, error) {
	env := map[string]string{}
	for _, name := range []string{"NODE_NAME", "POD_NAME", "POD_NAMESPACE"} {
		if env[name] = getenv(name); env[name] == "" {
			return nil, fmt.Errorf("environment variable %s is empty, but the DaemonSet sets it from the downward API", name)
		}
	}
	return env, nil
}
