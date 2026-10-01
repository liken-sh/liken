package main

// Reading spec.layout from the Sinks.
//
// Two containers read it. The declare init container reads it from the
// API server before PipeWire starts, and the operator container reads
// it from its watch on every pass. A Sink is named by the endpoint's
// device name, so both read the endpoints nameEndpoints named, the
// same way the slice does.

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/liken-sh/liken/kubernetes/apiclient"
	"github.com/liken-sh/liken/kubernetes/informer"
)

// declareAPITimeout bounds each request the declare container sends.
// PipeWire does not start until the container exits, so an API server
// that does not answer must cost the pod seconds, and the first
// failure ends the reads.
const declareAPITimeout = 5 * time.Second

// readSinks reads the Sink of every named playback endpoint. An
// endpoint whose Sink does not exist yet is left out, which is every
// endpoint on a machine whose operator has never run. Any other
// failure is an error, because a spec that did not read is not a spec
// that is empty.
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

// specLayouts holds each named sink's spec.layout, keyed by its
// address in the graph. A Sink with no layout is left out.
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
// The container falls back to the ELD and the channel map when it
// cannot read the Sinks, and says so in one line. A pod that waited
// for the API server would leave the machine with no sound while the
// control plane is down, and the operator container reads the specs
// again once it runs: a layout that differs is a layout change, which
// it applies when no stream plays (layoutdrift.go).
func declaredSpecs(outputs []alsaEndpoint) map[nodeAddress][]string {
	machine := os.Getenv("NODE_NAME")
	if machine == "" {
		fmt.Fprintf(os.Stderr, "NODE_NAME is unset, so no Sink names the outputs; "+
			"declaring the layouts from the ELD and the channel map alone\n")
		return nil
	}
	client, err := apiclient.InCluster(apiclient.InClusterOptions{Timeout: declareAPITimeout})
	if err != nil {
		fmt.Fprintf(os.Stderr, "in-cluster config: %v; "+
			"declaring the layouts from the ELD and the channel map alone\n", err)
		return nil
	}
	return apiSpecs(client, machine, outputs)
}

// apiSpecs reads every spec.layout from the API server, and reads
// none when one request fails.
func apiSpecs(client *apiclient.Client, machine string, outputs []alsaEndpoint) map[nodeAddress][]string {
	named, _ := nameEndpoints(machine, outputs)
	sinks, err := readSinks(named, func(name string) (*Sink, error) {
		return getSink(client, name)
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v; declaring the layouts from the ELD and the channel map alone\n", err)
		return nil
	}
	return specLayouts(named, sinks)
}

// cachedSinks reads the Sinks of the named endpoints for the operator
// container, from the watch's store where it holds a current copy.
func (e *endpointControl) cachedSinks(named []alsaEndpoint) (map[string]Sink, error) {
	return readSinks(named, func(name string) (*Sink, error) {
		return informer.ReadOne[Sink](e.client, e.cache.sinks, name, sinkPath(name))
	})
}
