package main

// Reads spec.layout from the Sinks. The declare init container reads it
// from the API server before PipeWire starts, and the operator container
// reads it on each reconcile pass. Both name the endpoints with
// nameEndpoints, so they look up each Sink under the same device name
// that the ResourceSlice uses.

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// declareAPITimeout is the timeout of each API request in the declare
// container. PipeWire does not start until that container exits, so an
// API server that does not respond would delay the machine's sound.
// The first failed request ends all the reads.
const declareAPITimeout = 5 * time.Second

// readSinks reads the Sink of each named playback endpoint. It skips an
// endpoint that has no Sink, as on a machine where the operator has not
// run yet. Any other failure is an error, because a spec that could not
// be read is not an empty spec.
func readSinks(named []alsaEndpoint, read func(name string) (*Sink, error)) (map[string]Sink, error) {
	sinks := map[string]Sink{}
	for _, output := range named {
		if output.Capture || output.Name() == "" {
			continue
		}
		sink, err := read(output.Name())
		if errors.Is(err, apiclient.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("reading the Sink %s: %w", output.Name(), err)
		}
		sinks[output.Name()] = *sink
	}
	return sinks, nil
}

// specLayouts maps the graph address of each named endpoint to its
// Sink's spec.layout. A Sink with no layout has no entry.
func specLayouts(named []alsaEndpoint, sinks map[string]Sink) map[nodeAddress][]string {
	specs := map[nodeAddress][]string{}
	for _, output := range named {
		if sink, ok := sinks[output.Name()]; ok && len(sink.Spec.Layout) > 0 {
			specs[output.graphAddress()] = sink.Spec.Layout
		}
	}
	return specs
}

// declaredSpecs reads every spec.layout for the declare container.
//
// When the Sinks cannot be read it prints why and returns nil, so the
// layouts come from the ELD and the channel map alone. Waiting for the
// API server would leave the machine without sound while the control
// plane is down. The operator container reads the specs again when it
// runs, and applies any difference when no stream is playing
// (layoutdrift.go).
func declaredSpecs(outputs []alsaEndpoint) map[nodeAddress][]string {
	machine := os.Getenv("NODE_NAME")
	if machine == "" {
		fmt.Fprintf(os.Stderr, "declaring the layouts from the ELD and the channel map alone: "+
			"NODE_NAME is unset, so no Sink names the outputs\n")
		return nil
	}
	client, err := apiclient.InCluster(apiclient.InClusterOptions{Timeout: declareAPITimeout})
	if err != nil {
		fmt.Fprintf(os.Stderr, "declaring the layouts from the ELD and the channel map alone: "+
			"reading the in-cluster config: %v\n", err)
		return nil
	}
	return apiSpecs(client, machine, outputs)
}

// apiSpecs reads every spec.layout from the API server. When one
// request fails it returns nil, not a partial map.
func apiSpecs(client *apiclient.Client, machine string, outputs []alsaEndpoint) map[nodeAddress][]string {
	named, _ := nameEndpoints(machine, outputs)
	sinks, err := readSinks(named, func(name string) (*Sink, error) {
		return getSink(client, name)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "declaring the layouts from the ELD and the channel map alone: %v\n", err)
		return nil
	}
	return specLayouts(named, sinks)
}

// cachedSinks reads the Sinks of the named endpoints for the operator
// container. Each Sink comes from the informer's store when the store is
// ready and current, and from the API server otherwise.
func (e *endpointControl) cachedSinks(named []alsaEndpoint) (map[string]Sink, error) {
	return readSinks(named, func(name string) (*Sink, error) {
		return informer.ReadOne[Sink](e.client, e.cache.sinks, name, sinkPath(name))
	})
}
