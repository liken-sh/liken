package main

// Which screens a claim holds, read from the API server.
//
// The Display pass restarts the compositor for two reasons: to put a
// screen back at its resting mode, and to heal a canvas. Either
// restart takes the screen away from the claim that holds it, so both
// wait while a claim holds a screen. The CDI specs on disk name the
// claims the kubelet prepared, and a claim whose prepare has not
// succeeded yet has no spec. That claim still holds its screen: the
// kubelet retries the prepare until the pod starts or goes away, and
// each prepare switches the screen to the claim's mode. A pass that
// read only the specs found the screen free and switched it back, and
// the next retry switched it again. Each switch restarted the
// compositor, and each restart waited longer in the kubelet's crash
// backoff.
//
// So a claim holds a screen when its allocation names the screen's
// output device in this node's pool, and a pod that the API server is
// not deleting holds the claim. The claims carry no field that selects
// a node, so the answer costs one listing of every claim in the
// cluster. The Display pass reads it only when a restart would follow,
// and at most once per pass. The slice pass reads it only while no
// compositor serves, to leave the taint off an output whose claim is
// still preparing (compositorDown in slices.go).

import (
	"errors"

	"github.com/liken-sh/liken/kubernetes/apiclient"
)

// allocatedOutputs names the output devices of this node that a live
// claim holds. A draw or a control device is not in the answer, for
// the reason preparedOutputs leaves them out: neither owns the mode.
func allocatedOutputs(client *apiclient.Client, node string, stores clusterStores) (map[string]bool, error) {
	claims, err := listResourceClaims(client)
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for _, claim := range claims {
		outputs := claimOutputs(claim, node)
		if len(outputs) == 0 {
			continue
		}
		live, err := heldByLivePod(client, stores, claim)
		if err != nil {
			return nil, err
		}
		if !live {
			continue
		}
		for _, output := range outputs {
			held[output] = true
		}
	}
	return held, nil
}

// claimOutputs names the output devices one claim was allocated from
// this driver's pool on this node.
func claimOutputs(claim ResourceClaim, node string) []string {
	if claim.Status.Allocation == nil {
		return nil
	}
	var outputs []string
	for _, result := range claim.Status.Allocation.Devices.Results {
		if result.Driver != DriverName || result.Pool != node {
			continue
		}
		if _, control := outputOfControl(result.Device); control {
			continue
		}
		if _, draw := outputOfDraw(result.Device); draw {
			continue
		}
		outputs = append(outputs, result.Device)
	}
	return outputs
}

// heldByLivePod reports whether a pod that the API server is not
// deleting holds the claim. The kubelet stops its prepare retries when
// the pod goes, and the claim's reservation can outlast the pod by a
// few seconds, so the pod is what says the claim is still wanted.
func heldByLivePod(client *apiclient.Client, stores clusterStores, claim ResourceClaim) (bool, error) {
	for _, consumer := range claim.Status.ReservedFor {
		if consumer.Resource != podsResource {
			continue
		}
		pod, err := stores.pod(client, claim.Metadata.Namespace, consumer.Name)
		if errors.Is(err, apiclient.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if pod.Metadata.DeletionTimestamp == nil {
			return true, nil
		}
	}
	return false, nil
}

// preparingOutputs names the output devices whose claim is still
// preparing: a live claim holds the output, and no spec on disk names
// it yet.
func preparingOutputs(allocated, prepared map[string]bool) map[string]bool {
	preparing := map[string]bool{}
	for output := range allocated {
		if !prepared[output] {
			preparing[output] = true
		}
	}
	return preparing
}

// screenHolds answers, for one pass, which output devices a claim
// holds. The specs on disk answer first, because reading them costs no
// request. The API server answers the rest, once, and only for a pass
// that asks.
type screenHolds struct {
	prepared  map[string]bool
	allocated func() (map[string]bool, error)

	read    bool
	claimed map[string]bool
	err     error
}

// holds reports whether a claim holds one output device.
func (h *screenHolds) holds(device string) (bool, error) {
	if h.prepared[device] {
		return true, nil
	}
	claimed, err := h.claims()
	return claimed[device], err
}

// any reports whether a claim holds any output device on this card.
func (h *screenHolds) any() (bool, error) {
	if len(h.prepared) > 0 {
		return true, nil
	}
	claimed, err := h.claims()
	return len(claimed) > 0, err
}

func (h *screenHolds) claims() (map[string]bool, error) {
	if !h.read && h.allocated != nil {
		h.claimed, h.err = h.allocated()
		h.read = true
	}
	return h.claimed, h.err
}
